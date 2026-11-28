use std::collections::HashSet;

use crate::local_db::{
    AiSummaryActionItem, AiSummaryResult, AiSummaryRun, LocalResult, MeetingJob, MeetingMember,
    MeetingMinutesPayload,
};

#[derive(Debug, Clone)]
pub struct SummaryExportSource {
    pub result: AiSummaryResult,
    pub minutes_payload: MeetingMinutesPayload,
    pub run_id: Option<String>,
    pub reconstructed_payload: bool,
}

pub fn resolve_summary_source(
    job: &MeetingJob,
    members: &[MeetingMember],
    requested_run_id: Option<&str>,
) -> LocalResult<SummaryExportSource> {
    if let Some(run_id) = requested_run_id
        .map(str::trim)
        .filter(|value| !value.is_empty())
    {
        let run = job
            .summary_runs
            .iter()
            .find(|run| run.id == run_id)
            .ok_or_else(|| format!("导出失败：没有找到指定的 AI 总结版本 {run_id}。"))?;
        return summary_source_from_run(job, members, run);
    }

    if let Some(active_run_id) = job.active_summary_run_id.as_deref() {
        let run = job
            .summary_runs
            .iter()
            .find(|run| run.id == active_run_id)
            .ok_or_else(|| "导出失败：当前活动的 AI 总结版本不存在。".to_string())?;
        return summary_source_from_run(job, members, run);
    }

    if has_legacy_summary(job) {
        let mut result = AiSummaryResult {
            title: job.title.clone(),
            overview: job.summary.overview.clone(),
            topics: job.summary.topics.clone(),
            decisions: job.summary.decisions.clone(),
            action_items: job
                .summary
                .action_items
                .iter()
                .map(|item| AiSummaryActionItem {
                    task: item.clone(),
                    owner: String::new(),
                    due_date: String::new(),
                })
                .collect(),
            risks: job.summary.risks.clone(),
            follow_ups: job.summary.follow_ups.clone(),
        };
        let payload = reconstruct_verified_payload(job, &result, members, "", None)?;
        make_payload_authoritative(&mut result);
        return Ok(SummaryExportSource {
            result,
            minutes_payload: payload,
            run_id: None,
            reconstructed_payload: false,
        });
    }

    Err("导出失败：当前任务没有可导出的 AI 总结。".into())
}

fn summary_source_from_run(
    job: &MeetingJob,
    members: &[MeetingMember],
    run: &AiSummaryRun,
) -> LocalResult<SummaryExportSource> {
    if run.status != "completed" {
        return Err(format!("导出失败：AI 总结版本 {} 尚未完成。", run.id));
    }

    let mut result = run
        .result
        .clone()
        .ok_or_else(|| format!("导出失败：AI 总结版本 {} 没有结果数据。", run.id))?;
    let (mut payload, reconstructed_payload) = match run.minutes_payload.clone() {
        Some(payload) => {
            validate_payload_source(&payload, &run.id)?;
            validate_speaker_coverage(job, &payload)?;
            (payload, false)
        }
        None => (
            reconstruct_verified_payload(
                job,
                &result,
                members,
                &run.template_id,
                Some(run.id.clone()),
            )?,
            true,
        ),
    };
    mark_missing_ai_speakers(job, &mut payload);
    make_payload_authoritative(&mut result);

    Ok(SummaryExportSource {
        result,
        minutes_payload: payload,
        run_id: Some(run.id.clone()),
        reconstructed_payload,
    })
}

pub(crate) fn reconstruct_verified_payload(
    job: &MeetingJob,
    result: &AiSummaryResult,
    members: &[MeetingMember],
    template_id: &str,
    source_summary_run_id: Option<String>,
) -> LocalResult<MeetingMinutesPayload> {
    let mut payload = super::derive_meeting_minutes_payload(
        job,
        result,
        members,
        template_id,
        source_summary_run_id,
    );
    super::add_missing_transcript_speakers(job, &mut payload, members);
    mark_missing_ai_speakers(job, &mut payload);
    let has_speech_content = payload.speaker_reports.iter().any(|report| {
        !report.weekly_summary.is_empty()
            || !report.next_week_plan.is_empty()
            || !report.summary.is_empty()
    });
    if !has_speech_content && payload.global_summary.is_empty() {
        let parsed = super::parse_overview_to_export_data(&result.overview);
        payload.global_summary =
            super::build_fallback_overview_items(&AiSummaryResult::default(), &parsed);
    }
    validate_speaker_coverage(job, &payload)?;
    Ok(payload)
}

fn validate_payload_source(payload: &MeetingMinutesPayload, run_id: &str) -> LocalResult<()> {
    if let Some(payload_run_id) = payload
        .source_summary_run_id
        .as_deref()
        .map(str::trim)
        .filter(|value| !value.is_empty())
    {
        if payload_run_id != run_id {
            return Err(format!(
                "导出失败：AI 总结版本 {run_id} 的结构化会议纪要来自其他版本 {payload_run_id}。"
            ));
        }
    }
    Ok(())
}

pub(crate) fn validate_speaker_coverage(
    job: &MeetingJob,
    payload: &MeetingMinutesPayload,
) -> LocalResult<()> {
    if !job.diarization_status.is_verified() {
        return Ok(());
    }
    let segments = &job.speaker_segments;
    let transcript_speakers = segments
        .iter()
        .filter_map(|segment| segment.speaker.as_deref())
        .map(normalize_speaker)
        .filter(|speaker| !speaker.is_empty())
        .collect::<HashSet<_>>();
    let participant_speakers = payload
        .participants
        .iter()
        .flat_map(|participant| [&participant.speaker_label, &participant.resolved_name])
        .map(|speaker| normalize_speaker(speaker))
        .filter(|speaker| !speaker.is_empty())
        .collect::<HashSet<_>>();
    let report_speakers = payload
        .speaker_reports
        .iter()
        .flat_map(|report| [&report.speaker_label, &report.resolved_name])
        .map(|speaker| normalize_speaker(speaker))
        .filter(|speaker| !speaker.is_empty())
        .collect::<HashSet<_>>();
    let mut missing = transcript_speakers
        .into_iter()
        .filter(|speaker| {
            !participant_speakers.contains(speaker) || !report_speakers.contains(speaker)
        })
        .collect::<Vec<_>>();
    missing.sort();
    if missing.is_empty() {
        Ok(())
    } else {
        Err(format!(
            "导出失败：结构化会议纪要缺少逐字稿讲话人：{}。",
            missing.join("、")
        ))
    }
}

fn make_payload_authoritative(result: &mut AiSummaryResult) {
    let mut lines = Vec::new();
    append_section(&mut lines, "决策", &result.decisions);
    let actions = result
        .action_items
        .iter()
        .filter(|item| !item.task.trim().is_empty())
        .map(|item| {
            format!(
                "任务：{}；负责人：{}；截止日期：{}",
                item.task.trim(),
                value_or_pending(&item.owner),
                value_or_pending(&item.due_date)
            )
        })
        .collect::<Vec<_>>();
    append_section(&mut lines, "行动项", &actions);
    append_section(&mut lines, "风险", &result.risks);
    append_section(&mut lines, "跟进事项", &result.follow_ups);
    result.overview = lines.join("\n");
}

pub(crate) fn mark_missing_ai_speakers(job: &MeetingJob, payload: &mut MeetingMinutesPayload) {
    if !job.diarization_status.is_verified() {
        return;
    }
    let segments = &job.speaker_segments;
    let transcript_speakers = segments
        .iter()
        .filter_map(|segment| segment.speaker.as_deref())
        .map(normalize_speaker)
        .filter(|speaker| !speaker.is_empty())
        .collect::<HashSet<_>>();
    for report in &mut payload.speaker_reports {
        let speaker = normalize_speaker(if report.speaker_label.trim().is_empty() {
            &report.resolved_name
        } else {
            &report.speaker_label
        });
        if transcript_speakers.contains(&speaker)
            && report.weekly_summary.is_empty()
            && report.next_week_plan.is_empty()
            && report.summary.is_empty()
        {
            report.match_status = "missing_from_ai".into();
            if let Some(participant) = payload.participants.iter_mut().find(|participant| {
                normalize_speaker(&participant.speaker_label) == speaker
                    || normalize_speaker(&participant.resolved_name) == speaker
            }) {
                participant.match_status = "missing_from_ai".into();
            }
        }
    }
}

fn append_section(lines: &mut Vec<String>, heading: &str, items: &[String]) {
    if items.iter().all(|item| item.trim().is_empty()) {
        return;
    }
    lines.push(format!("{heading}："));
    lines.extend(
        items
            .iter()
            .map(|item| item.trim())
            .filter(|item| !item.is_empty())
            .map(str::to_string),
    );
}

fn value_or_pending(value: &str) -> &str {
    let trimmed = value.trim();
    if trimmed.is_empty() {
        "待定"
    } else {
        trimmed
    }
}

fn normalize_speaker(value: &str) -> String {
    value
        .trim()
        .trim_start_matches('【')
        .trim_end_matches('】')
        .trim_end_matches("（未发言）")
        .trim_end_matches("(未发言)")
        .trim()
        .to_string()
}

fn has_legacy_summary(job: &MeetingJob) -> bool {
    !job.summary.overview.trim().is_empty()
        || !job.summary.topics.is_empty()
        || !job.summary.decisions.is_empty()
        || !job.summary.action_items.is_empty()
        || !job.summary.risks.is_empty()
        || !job.summary.follow_ups.is_empty()
}

#[cfg(test)]
mod tests {
    use super::*;

    fn completed_run(id: &str, title: &str) -> AiSummaryRun {
        AiSummaryRun {
            id: id.into(),
            job_id: "job-1".into(),
            status: "completed".into(),
            result: Some(AiSummaryResult {
                title: title.into(),
                overview: format!("会议名称：{title}"),
                ..AiSummaryResult::default()
            }),
            minutes_payload: Some(MeetingMinutesPayload::default()),
            ..AiSummaryRun::default()
        }
    }

    #[test]
    fn requested_run_is_the_export_authority() {
        let job = MeetingJob {
            active_summary_run_id: Some("run-active".into()),
            summary_runs: vec![
                completed_run("run-active", "活动版本"),
                completed_run("run-requested", "指定版本"),
            ],
            ..MeetingJob::default()
        };

        let source = resolve_summary_source(&job, &[], Some("run-requested")).unwrap();

        assert_eq!(source.run_id.as_deref(), Some("run-requested"));
        assert_eq!(source.result.title, "指定版本");
    }

    #[test]
    fn unresolved_requested_run_does_not_fall_back_to_another_version() {
        let job = MeetingJob {
            active_summary_run_id: Some("run-active".into()),
            summary_runs: vec![completed_run("run-active", "活动版本")],
            ..MeetingJob::default()
        };

        let error = resolve_summary_source(&job, &[], Some("run-missing")).unwrap_err();

        assert!(error.contains("run-missing"));
    }

    #[test]
    fn active_run_is_used_when_no_version_is_requested() {
        let job = MeetingJob {
            active_summary_run_id: Some("run-active".into()),
            summary_runs: vec![completed_run("run-active", "活动版本")],
            ..MeetingJob::default()
        };

        let source = resolve_summary_source(&job, &[], None).unwrap();

        assert_eq!(source.run_id.as_deref(), Some("run-active"));
    }

    #[test]
    fn export_rejects_payload_missing_a_transcript_speaker() {
        let mut run = completed_run("run-active", "活动版本");
        run.minutes_payload = Some(MeetingMinutesPayload::default());
        let job = MeetingJob {
            enable_speaker: true,
            diarization_status: crate::domain::asr::DiarizationStatus::Completed,
            speaker_segments: vec![crate::local_db::TranscriptSegment {
                speaker: Some("李兰".into()),
                ..crate::local_db::TranscriptSegment::default()
            }],
            active_summary_run_id: Some("run-active".into()),
            summary_runs: vec![run],
            ..MeetingJob::default()
        };

        let error = resolve_summary_source(&job, &[], None).unwrap_err();

        assert!(error.contains("李兰"));
    }

    #[test]
    fn degraded_diarization_does_not_require_speaker_payload() {
        let mut run = completed_run("run-active", "活动版本");
        run.minutes_payload = Some(MeetingMinutesPayload::default());
        let job = MeetingJob {
            enable_speaker: true,
            diarization_status: crate::domain::asr::DiarizationStatus::Unavailable,
            speaker_segments: vec![crate::local_db::TranscriptSegment {
                speaker: Some("历史残留标签".into()),
                ..crate::local_db::TranscriptSegment::default()
            }],
            active_summary_run_id: Some("run-active".into()),
            summary_runs: vec![run],
            ..MeetingJob::default()
        };

        assert!(resolve_summary_source(&job, &[], None).is_ok());
    }

    #[test]
    fn completed_run_without_payload_reconstructs_and_marks_missing_ai_speakers() {
        let mut run = completed_run("run-active", "活动版本");
        run.minutes_payload = None;
        run.result = Some(AiSummaryResult {
            title: "周会".into(),
            overview: "发言内容\n【部门】：人员A\n上周总结：\n1、A事项".into(),
            ..AiSummaryResult::default()
        });
        let job = MeetingJob {
            enable_speaker: true,
            diarization_status: crate::domain::asr::DiarizationStatus::Completed,
            speaker_segments: vec![
                crate::local_db::TranscriptSegment {
                    speaker: Some("人员A".into()),
                    ..crate::local_db::TranscriptSegment::default()
                },
                crate::local_db::TranscriptSegment {
                    speaker: Some("人员B".into()),
                    ..crate::local_db::TranscriptSegment::default()
                },
            ],
            active_summary_run_id: Some("run-active".into()),
            summary_runs: vec![run],
            ..MeetingJob::default()
        };

        let source = resolve_summary_source(&job, &[], None).unwrap();
        let speakers = source
            .minutes_payload
            .speaker_reports
            .iter()
            .map(|report| report.resolved_name.as_str())
            .collect::<Vec<_>>();

        assert!(source.reconstructed_payload);
        assert!(speakers.contains(&"人员A"));
        assert!(speakers.contains(&"人员B"));
        assert_eq!(
            source
                .minutes_payload
                .speaker_reports
                .iter()
                .find(|report| report.resolved_name == "人员B")
                .map(|report| report.match_status.as_str()),
            Some("missing_from_ai")
        );
    }

    #[test]
    fn reconstructed_payload_keeps_unstructured_overview_as_fallback_content() {
        let mut run = completed_run("run-active", "活动版本");
        run.minutes_payload = None;
        run.result = Some(AiSummaryResult {
            title: "周会".into(),
            overview: "旧记录中唯一的会议摘要正文。".into(),
            ..AiSummaryResult::default()
        });
        let job = MeetingJob {
            enable_speaker: true,
            diarization_status: crate::domain::asr::DiarizationStatus::Completed,
            speaker_segments: vec![crate::local_db::TranscriptSegment {
                speaker: Some("人员A".into()),
                ..crate::local_db::TranscriptSegment::default()
            }],
            active_summary_run_id: Some("run-active".into()),
            summary_runs: vec![run],
            ..MeetingJob::default()
        };

        let source = resolve_summary_source(&job, &[], None).unwrap();

        assert_eq!(
            source.minutes_payload.global_summary,
            ["旧记录中唯一的会议摘要正文。"]
        );
        assert_eq!(source.minutes_payload.speaker_reports.len(), 1);
        assert_eq!(
            source.minutes_payload.speaker_reports[0].resolved_name,
            "人员A"
        );
    }

    #[test]
    fn reconstructed_unstructured_body_survives_real_backfill_and_repeated_reads() {
        let mut run = completed_run("run-backfill", "合成AI标题");
        run.minutes_payload = None;
        run.result = Some(AiSummaryResult {
            title: "合成AI标题".into(),
            overview: "会议名称：合成产品周会\n会议时间：2026-11-20 10:00\n会议地点：合成会议室\n记录人：合成记录人\n出席人员：合成出席人\n缺席人员：合成缺席人\n会议主持人：合成主持人\n审阅：合成审阅人\nOriginal summary\n1、Second paragraph"
                .into(),
            ..AiSummaryResult::default()
        });
        let job = MeetingJob {
            id: "job-1700000000000-621".into(),
            title: "合成任务标题".into(),
            created_at: "2026-11-20T00:00:00Z".into(),
            active_summary_run_id: Some(run.id.clone()),
            ..MeetingJob::default()
        };
        run.job_id.clone_from(&job.id);
        let mut connection = rusqlite::Connection::open_in_memory().unwrap();
        crate::local_db::schema::apply_test_schema(&connection).unwrap();
        let transaction = connection.transaction().unwrap();
        transaction
            .execute(
                "INSERT INTO jobs(id,title,created_at,lang,summary_template,upload_status,
                 asr_status,summary_status,overall_status,hotwords_json,export_formats_json,active_summary_run_id)
                 VALUES(?1,?2,'2026-11-20T00:00:00Z','zh','','uploaded','completed','completed','completed','[]','[]',?3)",
                rusqlite::params![job.id, job.title, run.id],
            )
            .unwrap();
        crate::infrastructure::repositories::ai_summary_runs::save_summary_run_tx(
            &transaction,
            &run,
        )
        .unwrap();
        transaction.commit().unwrap();
        let mut stored = job.clone();
        stored.summary_runs =
            crate::infrastructure::repositories::ai_summary_runs::list_summary_runs(
                &connection,
                &job.id,
            )
            .unwrap();
        let first = resolve_summary_source(&stored, &[], None).unwrap();
        assert!(first.reconstructed_payload);
        assert_eq!(
            first.minutes_payload.global_summary,
            ["Original summary", "Second paragraph"]
        );
        let first_document = super::super::build_export_doc_data_from_source(&stored, &first, &[]);
        let expected_metadata = crate::domain::meeting_minutes::MeetingMetadata {
            meeting_name: "合成产品周会".into(),
            meeting_time: "2026-11-20 10:00".into(),
            meeting_location: "合成会议室".into(),
            recorder: "合成记录人".into(),
            attendees: "合成出席人".into(),
            absentees: "合成缺席人".into(),
            host: "合成主持人".into(),
            reviewer: "合成审阅人".into(),
        };
        assert_eq!(
            super::super::metadata_from_export_data(&first_document),
            expected_metadata
        );
        assert_eq!(first_document.title, "合成产品周会会议纪要");
        assert!(
            crate::infrastructure::repositories::ai_summary_runs::backfill_minutes_payload(
                &mut connection,
                &job.id,
                &run.id,
                &first.minutes_payload,
            )
            .unwrap()
        );
        for _ in 0..3 {
            stored.summary_runs =
                crate::infrastructure::repositories::ai_summary_runs::list_summary_runs(
                    &connection,
                    &job.id,
                )
                .unwrap();
            let repeated = resolve_summary_source(&stored, &[], None).unwrap();
            assert!(!repeated.reconstructed_payload);
            let repeated_document =
                super::super::build_export_doc_data_from_source(&stored, &repeated, &[]);
            assert_eq!(
                super::super::metadata_from_export_data(&repeated_document),
                expected_metadata
            );
            assert_eq!(repeated_document.title, first_document.title);
            assert_eq!(
                repeated_document.closing_summary,
                first_document.closing_summary
            );
            assert_eq!(
                repeated_document.fallback_overview,
                first_document.fallback_overview
            );
            let blocks = |document: &super::super::ExportDocData| {
                document
                    .speech_blocks
                    .iter()
                    .map(|block| {
                        (
                            block.department.clone(),
                            block.name.clone(),
                            block.weekly_summary.clone(),
                            block.next_week_plan.clone(),
                            block.summary.clone(),
                            block.original_index,
                        )
                    })
                    .collect::<Vec<_>>()
            };
            assert_eq!(blocks(&repeated_document), blocks(&first_document));
        }
    }

    #[test]
    fn reconstructed_payload_excludes_metadata_colon_variants_from_body() {
        for separator in ["：", ":", " \t：", " \t:"] {
            let mut run = completed_run("run-metadata-filter", "合成AI标题");
            run.minutes_payload = None;
            run.result.as_mut().unwrap().overview = format!(
                "会议名称{separator}合成产品周会\n会议时间{separator}2026-11-20 10:00\n会议地点{separator}合成会议室\n记录人{separator}合成记录人\n出席人员{separator}合成出席人\n缺席人员{separator}合成缺席人\n会议主持人{separator}合成主持人\n审阅{separator}合成审阅人\n主要议题{separator}合成议题\nOriginal summary\n1、Second paragraph\n会议名称是正文的一部分"
            );
            let job = MeetingJob {
                active_summary_run_id: Some(run.id.clone()),
                summary_runs: vec![run],
                ..MeetingJob::default()
            };
            let source = resolve_summary_source(&job, &[], None).unwrap();
            assert_eq!(
                source.minutes_payload.meeting_info.meeting_name,
                "合成产品周会"
            );
            assert_eq!(
                source.minutes_payload.global_summary,
                [
                    "Original summary",
                    "Second paragraph",
                    "会议名称是正文的一部分"
                ],
                "metadata separator {separator:?} entered the persisted body"
            );
        }
    }

    #[test]
    fn metadata_only_ooxml_is_identical_after_real_backfill() {
        use crate::domain::meeting_minutes::MeetingInfoSource;

        for separator in ["：", ":", " \t：", " \t:"] {
            for placeholders in [false, true] {
                let mut run = completed_run("run-metadata-only", "合成AI标题");
                run.minutes_payload = None;
                let mut overview = Vec::new();
                for (label, value) in [
                    ("会议名称", "合成产品周会"),
                    ("会议时间", "2026-11-20 10:00"),
                    ("会议地点", "合成会议室"),
                    ("记录人", "合成记录人"),
                    ("出席人员", "合成出席人"),
                    ("缺席人员", "合成缺席人"),
                    ("会议主持人", "合成主持人"),
                    ("审阅", "合成审阅人"),
                ] {
                    overview.push(format!(
                        "{label}{separator}{}",
                        if placeholders { "待补充" } else { value }
                    ));
                }
                run.result.as_mut().unwrap().overview = overview.join("\n");
                let mut job = MeetingJob {
                    id: "job-1700000000000-632".into(),
                    title: "合成任务标题".into(),
                    active_summary_run_id: Some(run.id.clone()),
                    ..MeetingJob::default()
                };
                run.job_id.clone_from(&job.id);
                let original_overview = run.result.as_ref().unwrap().overview.clone();
                let mut connection = rusqlite::Connection::open_in_memory().unwrap();
                crate::local_db::schema::apply_test_schema(&connection).unwrap();
                let transaction = connection.transaction().unwrap();
                transaction
                    .execute(
                        "INSERT INTO jobs(id,title,created_at,lang,summary_template,upload_status,
                         asr_status,summary_status,overall_status,hotwords_json,export_formats_json,active_summary_run_id)
                         VALUES(?1,?2,'2026-11-20T00:00:00Z','zh','','uploaded','completed','completed','completed','[]','[]',?3)",
                        rusqlite::params![job.id, job.title, run.id],
                    )
                    .unwrap();
                crate::infrastructure::repositories::ai_summary_runs::save_summary_run_tx(
                    &transaction,
                    &run,
                )
                .unwrap();
                transaction.commit().unwrap();
                let mut first_xml = None;
                for attempt in 0..4 {
                    job.summary_runs =
                        crate::infrastructure::repositories::ai_summary_runs::list_summary_runs(
                            &connection,
                            &job.id,
                        )
                        .unwrap();
                    let source = resolve_summary_source(&job, &[], None).unwrap();
                    assert_eq!(source.reconstructed_payload, attempt == 0);
                    assert!(source.minutes_payload.global_summary.is_empty());
                    assert_eq!(
                        source.minutes_payload.meeting_info_source,
                        if placeholders {
                            MeetingInfoSource::Empty
                        } else {
                            MeetingInfoSource::Ai
                        }
                    );
                    assert_eq!(
                        source.minutes_payload.source_summary_run_id,
                        Some(run.id.clone())
                    );
                    let document =
                        super::super::build_export_doc_data_from_source(&job, &source, &[]);
                    assert_eq!(
                        document.meeting_name,
                        if placeholders {
                            "合成AI标题"
                        } else {
                            "合成产品周会"
                        }
                    );
                    assert_eq!(
                        document.meeting_time,
                        if placeholders { "" } else { "2026-11-20 10:00" }
                    );
                    assert_eq!(
                        document.meeting_location,
                        if placeholders { "" } else { "合成会议室" }
                    );
                    let xml = encoded_minutes_document_xml(&document);
                    assert!(
                        !xml.contains("决策、行动项、风险与跟进："),
                        "metadata-only appendix changed at attempt {attempt}, separator {separator:?}"
                    );
                    if let Some(first_xml) = &first_xml {
                        assert_eq!(strict_minutes_xml_events(&xml), *first_xml);
                    } else {
                        first_xml = Some(strict_minutes_xml_events(&xml));
                    }
                    assert_eq!(
                        job.summary_runs[0].result.as_ref().unwrap().overview,
                        original_overview
                    );
                    assert_eq!(
                        crate::infrastructure::repositories::ai_summary_runs::backfill_minutes_payload(
                            &mut connection,
                            &job.id,
                            &run.id,
                            &source.minutes_payload,
                        )
                        .unwrap(),
                        attempt == 0
                    );
                }
            }
        }
    }

    fn encoded_minutes_document_xml(document: &super::super::ExportDocData) -> String {
        let mut archive = zip::ZipArchive::new(std::io::Cursor::new(
            super::super::renderer::TEMPLATE_DOCX_BYTES,
        ))
        .unwrap();
        let mut template = String::new();
        std::io::Read::read_to_string(
            &mut archive.by_name("word/document.xml").unwrap(),
            &mut template,
        )
        .unwrap();
        super::super::renderer::render_document_xml(&template, document).unwrap()
    }

    fn strict_minutes_xml_events(xml: &str) -> Vec<xml::reader::XmlEvent> {
        use xml::reader::{EventReader, ParserConfig, XmlEvent};

        EventReader::new_with_config(xml.as_bytes(), ParserConfig::new().ignore_comments(false))
            .into_iter()
            .map(|event| {
                let mut event = event.unwrap();
                if let XmlEvent::StartElement { attributes, .. } = &mut event {
                    attributes.sort_by(|left, right| {
                        (
                            &left.name.namespace,
                            &left.name.local_name,
                            &left.name.prefix,
                        )
                            .cmp(&(
                                &right.name.namespace,
                                &right.name.local_name,
                                &right.name.prefix,
                            ))
                    });
                }
                event
            })
            .collect()
    }

    #[test]
    fn minutes_xml_comparison_preserves_namespaces_values_and_text() {
        let original = strict_minutes_xml_events(
            r#"<root xmlns:w="urn:word" w:id="value" id="plain"> Body <!--note--><w:child/></root>"#,
        );
        assert_eq!(
            original,
            strict_minutes_xml_events(
                r#"<root id="plain" w:id="value" xmlns:w="urn:word"> Body <!--note--><w:child/></root>"#
            )
        );
        for changed in [
            r#"<root xmlns:w="urn:other" w:id="value" id="plain"> Body <!--note--><w:child/></root>"#,
            r#"<root xmlns:w="urn:word" w:other="value" id="plain"> Body <!--note--><w:child/></root>"#,
            r#"<root xmlns:w="urn:word" w:id="other" id="plain"> Body <!--note--><w:child/></root>"#,
            r#"<root xmlns:w="urn:word" w:id="value" id="plain"> Body<!--note--><w:child/></root>"#,
            r#"<root xmlns:w="urn:word" w:id="value" id="plain"> Body <!--other--><w:child/></root>"#,
            r#"<root xmlns:w="urn:word" w:id="value" id="plain"> Body <!--note--></root>"#,
        ] {
            assert_ne!(original, strict_minutes_xml_events(changed));
        }
    }

    #[test]
    fn persisted_metadata_authority_never_restores_stale_overview() {
        use crate::{domain::meeting_minutes::MeetingInfoSource, local_db::MeetingMinutesInfo};

        for (source, name) in [
            (MeetingInfoSource::Ai, "合成已存会议名"),
            (MeetingInfoSource::Ai, ""),
            (MeetingInfoSource::Empty, ""),
            (MeetingInfoSource::User, "合成用户会议名"),
            (MeetingInfoSource::User, ""),
        ] {
            let mut run = completed_run("run-metadata-authority", "合成AI标题");
            run.result.as_mut().unwrap().overview =
                "会议名称：旧会议名\n会议时间：旧时间\n会议地点：旧地点\n记录人：旧记录人\n会议主持人：旧主持人\nStale body".into();
            let expected = MeetingMinutesInfo {
                meeting_name: name.into(),
                ..MeetingMinutesInfo::default()
            };
            run.minutes_payload = Some(MeetingMinutesPayload {
                schema_version: 2,
                meeting_info_source: source,
                source_summary_run_id: Some(run.id.clone()),
                meeting_info: expected.clone(),
                ..MeetingMinutesPayload::default()
            });
            let before = serde_json::to_value(&run.minutes_payload).unwrap();
            let job = MeetingJob {
                title: "合成任务标题".into(),
                active_summary_run_id: Some(run.id.clone()),
                summary_runs: vec![run],
                ..MeetingJob::default()
            };
            let mut first_xml = None;
            for _ in 0..3 {
                let resolved = resolve_summary_source(&job, &[], None).unwrap();
                assert!(!resolved.reconstructed_payload);
                let document =
                    super::super::build_export_doc_data_from_source(&job, &resolved, &[]);
                let mut expected = super::super::metadata_from_minutes_info(&expected);
                if expected.meeting_name.is_empty() {
                    expected.meeting_name = "合成AI标题".into();
                }
                assert_eq!(super::super::metadata_from_export_data(&document), expected);
                assert!(document.closing_summary.is_empty());
                assert!(document.fallback_overview.is_empty());
                assert!(document.speech_blocks.is_empty());
                let xml = encoded_minutes_document_xml(&document);
                assert!(!xml.contains("Stale body"));
                assert!(!xml.contains("决策、行动项、风险与跟进："));
                if let Some(first_xml) = &first_xml {
                    assert_eq!(strict_minutes_xml_events(&xml), *first_xml);
                } else {
                    first_xml = Some(strict_minutes_xml_events(&xml));
                }
                assert_eq!(
                    serde_json::to_value(&job.summary_runs[0].minutes_payload).unwrap(),
                    before
                );
            }
        }
    }

    #[test]
    fn persisted_empty_payload_never_resurrects_old_unstructured_body() {
        let mut run = completed_run("run-cleared", "Synthetic meeting");
        run.result = Some(AiSummaryResult {
            overview: "Stale unstructured summary".into(),
            ..AiSummaryResult::default()
        });
        run.minutes_payload = Some(MeetingMinutesPayload {
            source_summary_run_id: Some(run.id.clone()),
            ..MeetingMinutesPayload::default()
        });
        let job = MeetingJob {
            active_summary_run_id: Some(run.id.clone()),
            summary_runs: vec![run],
            ..MeetingJob::default()
        };
        let source = resolve_summary_source(&job, &[], None).unwrap();
        assert!(!source.reconstructed_payload);
        assert!(source.minutes_payload.global_summary.is_empty());
        let document = super::super::build_export_doc_data_from_source(&job, &source, &[]);
        assert!(document.fallback_overview.is_empty());
        assert!(document.closing_summary.is_empty());
        assert!(document.speech_blocks.is_empty());
    }
}
