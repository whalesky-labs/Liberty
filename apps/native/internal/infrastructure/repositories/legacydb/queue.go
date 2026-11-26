package legacydb

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"strings"

	"liberty.local/liberty/internal/domain"
	"modernc.org/sqlite"
)

func init() {
	// SQLite's built-in lower() only folds ASCII. Preserve QueryJobs' Go
	// Unicode substring semantics, including literal '%' and '_' searches.
	err := sqlite.RegisterDeterministicScalarFunction("liberty_contains_fold", 2, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		text, _ := args[0].(string)
		query, _ := args[1].(string)
		if strings.Contains(strings.ToLower(text), query) {
			return int64(1), nil
		}
		return int64(0), nil
	})
	if err != nil {
		panic(err)
	}
}

// QueryJobs loads only the requested page's rich projections and source files.
// Counts, search and paging share a read transaction; deleting a job cannot
// produce a count/page from two different committed states.
func (r *Reader) QueryJobs(ctx context.Context, q domain.QueueQuery) (domain.JobPage, error) {
	if q.Source != "local" {
		return domain.QueryJobs(nil, q), nil
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return domain.JobPage{}, ErrDatabase
	}
	defer tx.Rollback()
	page := domain.JobPage{Source: q.Source, Items: []domain.JobSummary{}}
	counts := &page.Counts
	err = tx.QueryRowContext(ctx, `SELECT COUNT(*),
		COALESCE(SUM(overall_status IN ('queued','transcribing','speaker_processing','summarizing')),0),
		COALESCE(SUM(overall_status='completed'),0), COALESCE(SUM(overall_status='failed'),0)`+visibleJobs).
		Scan(&counts.Total, &counts.Processing, &counts.Completed, &counts.Failed)
	if err != nil {
		return domain.JobPage{}, ErrDatabase
	}
	where := visibleJobs
	var args []any
	switch q.Filter {
	case "processing":
		where += " AND overall_status IN ('queued','transcribing','speaker_processing','summarizing')"
	case "completed", "failed":
		where += " AND overall_status = ?"
		args = append(args, q.Filter)
	}
	if search := strings.ToLower(strings.TrimSpace(q.Search)); search != "" {
		where += ` AND (liberty_contains_fold(title, ?) OR EXISTS (
			SELECT 1 FROM job_source_files source WHERE source.job_id=jobs.id AND liberty_contains_fold(source.name, ?)))`
		args = append(args, search, search)
	}
	if tx.QueryRowContext(ctx, "SELECT COUNT(*)"+where, args...).Scan(&page.Total) != nil {
		return domain.JobPage{}, ErrDatabase
	}
	page.PageCount = max(1, (page.Total+domain.QueuePageSize-1)/domain.QueuePageSize)
	page.Page = min(max(q.Page, 1), page.PageCount)
	start := (page.Page - 1) * domain.QueuePageSize
	page.To = min(start+domain.QueuePageSize, page.Total)
	if page.Total > 0 {
		page.From = start + 1
	}
	args = append(args, domain.QueuePageSize, start)
	rows, err := tx.QueryContext(ctx, "SELECT "+jobColumns+where+" ORDER BY created_at DESC LIMIT ? OFFSET ?", args...)
	if err != nil {
		return domain.JobPage{}, ErrDatabase
	}
	projection := newJobRow()
	for rows.Next() {
		job, err := projection.read(rows)
		if err != nil {
			rows.Close()
			return domain.JobPage{}, ErrDatabase
		}
		page.Items = append(page.Items, job)
	}
	rowErr := rows.Err()
	if rows.Close() != nil || rowErr != nil {
		return domain.JobPage{}, ErrDatabase
	}
	if len(page.Items) > 0 {
		ids, placeholders := make([]any, len(page.Items)), make([]string, len(page.Items))
		indexes := make(map[string]int, len(page.Items))
		for i, job := range page.Items {
			ids[i], placeholders[i], indexes[job.ID] = job.ID, "?", i
		}
		rows, err = tx.QueryContext(ctx, `SELECT job_id,id,name,kind,size_label FROM job_source_files WHERE job_id IN (`+strings.Join(placeholders, ",")+`) ORDER BY rowid ASC`, ids...)
		if err != nil {
			return domain.JobPage{}, ErrDatabase
		}
		var id string
		var file domain.SourceFile
		targets := []any{&id, &file.ID, &file.Name, &file.Kind, &file.SizeLabel}
		for rows.Next() {
			file = domain.SourceFile{}
			if rows.Scan(targets...) != nil {
				rows.Close()
				return domain.JobPage{}, ErrDatabase
			}
			i := indexes[id]
			page.Items[i].SourceFiles = append(page.Items[i].SourceFiles, file)
		}
		rowErr = rows.Err()
		if rows.Close() != nil || rowErr != nil {
			return domain.JobPage{}, ErrDatabase
		}
	}
	if tx.Commit() != nil {
		return domain.JobPage{}, ErrDatabase
	}
	return page, nil
}
