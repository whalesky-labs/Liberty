package schema

import (
	"database/sql"
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"testing"
)

func snapshot(t *testing.T, db *sql.DB) map[string][]string {
	t.Helper()
	rows, err := db.Query("SELECT name,sql,type FROM sqlite_master WHERE type IN ('table','index') AND sql IS NOT NULL AND name NOT LIKE 'sqlite_%' ORDER BY name")
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	result := map[string][]string{}
	space := regexp.MustCompile(`\s+`)
	for rows.Next() {
		var name, ddl, kind string
		if rows.Scan(&name, &ddl, &kind) != nil {
			t.Fatal("schema scan")
		}
		if !regexp.MustCompile(`^[a-z_]+$`).MatchString(name) {
			t.Fatal("unexpected table")
		}
		if kind == "table" {
			tables = append(tables, name)
		}
		result["schema:"+name] = []string{space.ReplaceAllString(strings.TrimSpace(ddl), " ")}
	}
	if rows.Err() != nil {
		t.Fatal(rows.Err())
	}
	rows.Close()
	for _, table := range tables {
		rows, err := db.Query("SELECT * FROM " + table)
		if err != nil {
			t.Fatal(err)
		}
		cols, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		result[table] = []string{}
		for rows.Next() {
			values := make([]any, len(cols))
			refs := make([]any, len(cols))
			for i := range values {
				refs[i] = &values[i]
			}
			if rows.Scan(refs...) != nil {
				t.Fatal("data scan")
			}
			for i, name := range cols {
				if (name == "api_token_ref" || name == "api_key_ref") && values[i] != "" {
					values[i] = "<system-reference>"
				}
				if table == "pet_profile" && name == "updated_at" {
					values[i] = "<migration-time>"
				}
			}
			data, err := json.Marshal(values)
			if err != nil {
				t.Fatal(err)
			}
			result[table] = append(result[table], string(data))
		}
		if rows.Err() != nil {
			t.Fatal(rows.Err())
		}
		rows.Close()
		sort.Strings(result[table])
	}
	return result
}
