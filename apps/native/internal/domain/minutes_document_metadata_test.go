package domain_test

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/domain"
	"liberty.local/liberty/internal/infrastructure/minutesdocx"
	"liberty.local/liberty/internal/infrastructure/repositories/jobdb"
	"liberty.local/liberty/internal/infrastructure/repositories/legacydb"
)

func syntheticMinutesMetadata() domain.MinutesInfo {
	return domain.MinutesInfo{MeetingName: "合成产品周会", MeetingTime: "2026-11-20 10:00", MeetingLocation: "合成会议室", Recorder: "合成记录人", Attendees: "合成出席人", Absentees: "合成缺席人", Host: "合成主持人", Reviewer: "合成审阅人"}
}

func syntheticMinutesOverview(separator string) string {
	return syntheticMinutesMetadataOverview(separator, syntheticMinutesMetadata()) + "\n主要议题" + separator + "合成议题\nOriginal summary\n1、Second paragraph\n会议名称是正文的一部分"
}

func syntheticMinutesMetadataOverview(separator string, metadata domain.MinutesInfo) string {
	lines := []string{}
	for _, field := range []struct{ label, value string }{{"会议名称", metadata.MeetingName}, {"会议时间", metadata.MeetingTime}, {"会议地点", metadata.MeetingLocation}, {"记录人", metadata.Recorder}, {"出席人员", metadata.Attendees}, {"缺席人员", metadata.Absentees}, {"会议主持人", metadata.Host}, {"审阅", metadata.Reviewer}} {
		lines = append(lines, field.label+separator+field.value)
	}
	return strings.Join(lines, "\n")
}

func encodedMinutesDocumentXML(t *testing.T, document domain.MinutesDocument) []byte {
	t.Helper()
	data, err := minutesdocx.Encode(document)
	if err != nil {
		t.Fatal(err)
	}
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	part, err := archive.Open("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	defer part.Close()
	xml, err := io.ReadAll(part)
	if err != nil {
		t.Fatal(err)
	}
	return xml
}

func TestMinutesDocumentMetadataSurvivesSQLiteBackfill(t *testing.T) {
	for _, separator := range []string{"：", ":", " \t：", " \t:"} {
		for _, sample := range []string{"body", "metadata-only", "placeholders"} {
			t.Run(sample+"/"+separator, func(t *testing.T) {
				ctx := context.Background()
				database := filepath.Join(t.TempDir(), "synthetic-minutes.sqlite3")
				store, _, err := jobdb.OpenPrepared(ctx, database, nil, true)
				if err != nil {
					t.Fatal(err)
				}
				var reader *legacydb.Reader
				t.Cleanup(func() {
					if reader != nil {
						_ = reader.Close()
					}
					if store != nil {
						_ = store.Close()
					}
				})
				jobID, runID := "job-1700000000000-631", "synthetic-metadata-run"
				result := domain.SummaryResult{Title: "合成AI标题", Overview: syntheticMinutesOverview(separator), Topics: []string{}, Decisions: []string{"Synthetic decision"}, ActionItems: []domain.SummaryAction{}, Risks: []string{}, FollowUps: []string{}}
				expectedInfo, expectedSource := syntheticMinutesMetadata(), "ai"
				expectedGlobal := []string{"Original summary", "Second paragraph", "会议名称是正文的一部分"}
				if sample != "body" {
					result.Overview = syntheticMinutesMetadataOverview(separator, expectedInfo)
					result.Decisions = []string{}
					expectedGlobal = []string{}
				}
				if sample == "placeholders" {
					result.Overview = syntheticMinutesMetadataOverview(separator, domain.MinutesInfo{MeetingName: "待补充", MeetingTime: "待补充", MeetingLocation: "待补充", Recorder: "待补充姓名", Attendees: "待补充姓名", Absentees: "待补充姓名", Host: "待补充姓名", Reviewer: "待补充姓名"})
					expectedInfo, expectedSource = domain.MinutesInfo{MeetingName: result.Title}, "empty"
				}
				encoded, err := json.Marshal(result)
				if err != nil {
					t.Fatal(err)
				}
				seed, err := sql.Open("sqlite", database)
				if err != nil {
					t.Fatal(err)
				}
				defer seed.Close()
				if _, err := seed.Exec(`INSERT INTO jobs(id,title,created_at,lang,summary_template,upload_status,asr_status,summary_status,overall_status,hotwords_json,export_formats_json,active_summary_run_id) VALUES(?,?,'2026-11-20T00:00:00Z','zh','','uploaded','completed','completed','completed','[]','[]',?)`, jobID, "合成任务标题", runID); err != nil {
					t.Fatal(err)
				}
				if _, err := seed.Exec(`INSERT INTO ai_summary_runs(id,job_id,status,result_json,created_at,updated_at) VALUES(?,?,'completed',?,'2026-11-20T00:00:00Z','2026-11-20T00:00:00Z')`, runID, jobID, string(encoded)); err != nil {
					t.Fatal(err)
				}
				if err := seed.Close(); err != nil {
					t.Fatal(err)
				}
				var first domain.MinutesDocument
				var firstXML []byte
				var persisted domain.MinutesPayload
				for attempt := range 4 {
					if attempt > 0 {
						if reader.Close() != nil || store.Close() != nil {
							t.Fatal("synthetic database close failed")
						}
						store, err = jobdb.Open(ctx, database)
						if err != nil {
							t.Fatal(err)
						}
					}
					reader, err = legacydb.Open(ctx, database)
					if err != nil {
						t.Fatal(err)
					}
					document, err := application.NewWordExport(application.NewMeetings(reader), store).Prepare(ctx, jobID, runID)
					if err != nil {
						t.Fatal(err)
					}
					if document.Info != expectedInfo || document.Title != expectedInfo.MeetingName+"会议纪要" {
						t.Errorf("export %d lost effective meeting metadata: %+v / %s", attempt, document.Info, document.Title)
					}
					if !reflect.DeepEqual(document.ClosingSummary, expectedGlobal) || sample == "body" && !strings.Contains(strings.Join(document.FallbackOverview, "\n"), "Synthetic decision") {
						t.Errorf("export %d polluted the body or lost structured decisions: %+v", attempt, document)
					}
					xml := encodedMinutesDocumentXML(t, document)
					if sample != "body" && (len(document.FallbackOverview) != 0 || bytes.Contains(xml, []byte("决策、行动项、风险与跟进："))) {
						t.Errorf("export %d leaked metadata-only input into the OOXML appendix", attempt)
					}
					run, err := store.SummaryRun(ctx, jobID, runID)
					if err != nil || run.MinutesPayload == nil || run.MinutesPayload.SourceSummaryRunID == nil || *run.MinutesPayload.SourceSummaryRunID != runID || run.Result == nil || run.Result.Overview != result.Overview {
						t.Fatal("backfill lost its run binding or rewrote the original result", err)
					}
					persistedInfo := expectedInfo
					if sample == "placeholders" {
						persistedInfo = domain.MinutesInfo{}
					}
					if run.MinutesPayload.MeetingInfo != persistedInfo || run.MinutesPayload.MeetingInfoSource != expectedSource {
						t.Fatal("backfill did not retain the parsed AI metadata")
					}
					if attempt == 0 {
						first, firstXML, persisted = document, xml, run.MinutesPayload.Clone()
					} else if !bytes.Equal(xml, firstXML) || !reflect.DeepEqual(document, first) || !reflect.DeepEqual(*run.MinutesPayload, persisted) {
						t.Fatal("repeated export changed the document or persisted payload")
					}
				}
			})
		}
	}
}

func TestMinutesDocumentMetadataExistingPayloadAuthority(t *testing.T) {
	for _, sample := range []struct {
		name, source string
		info         domain.MinutesInfo
	}{
		{"ai", "ai", syntheticMinutesMetadata()},
		{"ai-cleared", "ai", domain.MinutesInfo{}},
		{"empty", "empty", domain.MinutesInfo{}},
		{"user", "user", domain.MinutesInfo{MeetingName: "合成用户会议名"}},
		{"user-cleared", "user", domain.MinutesInfo{}},
	} {
		t.Run(sample.name, func(t *testing.T) {
			runID := "synthetic-authoritative-run"
			payload := domain.MinutesPayload{SchemaVersion: 2, MeetingInfoSource: sample.source, SourceSummaryRunID: &runID, MeetingInfo: sample.info, GlobalSummary: []string{}}
			before := payload.Clone()
			result := domain.Result{Job: domain.JobSummary{Title: "合成任务标题", ActiveSummaryRunID: &runID}, SummaryRuns: []domain.SummaryRun{{ID: runID, Status: "completed", Result: &domain.SummaryResult{Title: "合成AI标题", Overview: syntheticMinutesOverview(":")}, MinutesPayload: &payload}}}
			var firstXML []byte
			for range 3 {
				source, err := domain.ResolveSummaryExport(result, nil, runID)
				if err != nil || source.ReconstructedPayload {
					t.Fatal("existing payload was reconstructed", err)
				}
				document := domain.BuildMinutesDocument(result, source, nil)
				expected := sample.info
				if expected.MeetingName == "" {
					expected.MeetingName = "合成AI标题"
				}
				if document.Info != expected || len(document.ClosingSummary) != 0 || len(document.FallbackOverview) != 0 || len(document.SpeechBlocks) != 0 || !reflect.DeepEqual(payload.Clone(), before) {
					t.Fatal("existing metadata or cleared body lost authority", document)
				}
				xml := encodedMinutesDocumentXML(t, document)
				if bytes.Contains(xml, []byte("Original summary")) || bytes.Contains(xml, []byte("决策、行动项、风险与跟进：")) {
					t.Fatal("existing payload restored stale content in OOXML")
				}
				if firstXML == nil {
					firstXML = xml
				} else if !bytes.Equal(firstXML, xml) {
					t.Fatal("existing payload produced different OOXML on repeated export")
				}
			}
		})
	}
}

func TestMinutesDocumentMetadataLegacyProjectionUnchanged(t *testing.T) {
	for _, fresh := range []bool{false, true} {
		payload := domain.MinutesPayload{SchemaVersion: 1, MeetingInfoSource: "ai", MeetingInfo: domain.MinutesInfo{MeetingName: "合成旧会议", MeetingTime: "2026-11-19 09:00", MeetingLocation: "合成旧会议室"}}
		summary := domain.SummaryResult{Title: "合成AI标题"}
		expected := payload.MeetingInfo
		if fresh {
			summary.Overview = syntheticMinutesOverview(":")
			expected = syntheticMinutesMetadata()
		}
		document := domain.BuildMinutesDocument(domain.Result{}, domain.SummaryExportSource{Result: summary, MinutesPayload: payload}, nil)
		if document.Info != expected {
			t.Fatal("schema 1 AI priority or legacy fallback changed", fresh, document.Info)
		}
	}
}
