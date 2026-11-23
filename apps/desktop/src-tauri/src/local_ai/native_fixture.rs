//! Synthetic cross-language fixtures; never resolves providers or credentials.
use super::{client, prompt, response, AiChatCompletionInput, GenerateAiSummaryInput};
use crate::{domain::asr::DiarizationStatus, local_db::*};
use serde_json::{json, Value};

fn envelope(content: Value, reason: Option<&str>, parts: bool) -> String {
    let text = serde_json::to_string(&content).unwrap();
    let content = if parts {
        json!([{"type":"text","text":text},{"text":null},{}])
    } else {
        json!(text)
    };
    json!({"choices":[{"finish_reason":reason,"message":{"content":content}}]}).to_string()
}

#[test]
#[ignore = "run pnpm native:compat with an isolated fixture directory"]
fn export_native_ai_fixtures() {
    let directory = std::env::var_os("LIBERTY_NATIVE_FIXTURE_DIR").expect("fixture directory");
    let directory = std::path::PathBuf::from(directory);
    assert!(directory.is_dir());
    let mut prompts = Vec::new();
    let mut executions = Vec::new();
    for index in 0..12 {
        let mut job = MeetingJob {
            id: "job-1700000000000-1".into(),
            source: "local".into(),
            title: "合成周会 <&>".into(),
            lang: "zh".into(),
            hotwords: vec!["合成词".into(), "A&B".into()],
            diarization_status: DiarizationStatus::Completed,
            ..Default::default()
        };
        job.transcript_segments = vec![TranscriptSegment {
            id: "transcript".into(),
            text: "原始转写".into(),
            ..Default::default()
        }];
        job.speaker_segments = vec![
            TranscriptSegment {
                id: "1".into(),
                start_ms: 86_400_100,
                end_ms: 90_061_000,
                speaker: Some(" 甲<&>\u{2028} ".into()),
                text: " 合成发言 ".into(),
            },
            TranscriptSegment {
                id: "2".into(),
                start_ms: 100,
                end_ms: 200,
                speaker: None,
                text: "无姓名发言".into(),
            },
            TranscriptSegment {
                id: "3".into(),
                start_ms: 100,
                end_ms: 200,
                speaker: Some("乙\\u2028".into()),
                text: "  ".into(),
            },
        ];
        if index == 8 {
            job.diarization_status = DiarizationStatus::LegacyUnverified;
        }
        if index == 9 {
            job.speaker_segments.clear();
            job.transcript_segments.clear();
        }
        if index == 10 {
            job.transcript_segments[0].text = "甲: 合成发言\n乙: 合成内容\n".repeat(6_000);
            job.speaker_segments.clear();
        }
        if index == 11 {
            job.speaker_segments[0].text = "字".repeat(15_000);
        }
        let members = vec![
            MeetingMember {
                id: "m1".into(),
                name: "甲".into(),
                department: " 研发 ".into(),
                sort_order: 2,
                ..Default::default()
            },
            MeetingMember {
                id: "m2".into(),
                name: "乙".into(),
                department: " ".into(),
                sort_order: -1,
                is_recorder: true,
                ..Default::default()
            },
            MeetingMember {
                id: "m3".into(),
                name: "丙".into(),
                sort_order: 2,
                ..Default::default()
            },
        ];
        let template = AiSummaryTemplate {
            id: "synthetic-template".into(),
            prompt: "  合成模板\n保留原文。  ".into(),
            ..Default::default()
        };
        let input = GenerateAiSummaryInput {
            job: job.clone(),
            template: template.clone(),
            include_speaker: index & 1 != 0,
            include_timestamp: index & 2 != 0,
            use_member_mapping: index & 4 != 0,
            members: members.clone(),
            extra_instructions: "  保留决策  ".into(),
        };
        let built = prompt::build_summary_prompt_preview(&input);
        let planned = client::plan_summary_request(&AiChatCompletionInput {
            base_url: String::new(),
            api_key: String::new(),
            model: String::new(),
            system_prompt: built.system.clone(),
            user_prompt: built.user.clone(),
        })
        .unwrap();
        let transcript_json = serde_json::to_string(
            if job.diarization_status.is_verified() && !job.speaker_segments.is_empty() {
                &job.speaker_segments
            } else {
                &job.transcript_segments
            },
        )
        .unwrap();
        let transcript_sha256 = format!(
            "{:x}",
            <sha2::Sha256 as sha2::Digest>::digest(transcript_json.as_bytes())
        );
        let snapshot = json!({"schemaVersion":1,"job":job,"modelBaseUrl":"https://example.test","modelName":"synthetic","templateId":template.id,"members":members,"requiredSpeakers":planned.required_speakers,"fallbackTitle":planned.fallback_title,"systemPrompt":built.system});
        executions.push(json!({"Run":{"id":format!("summary-run-1700000000000-{index}"),"jobId":job.id,"templateId":template.id},"TranscriptJSON":transcript_json,"TranscriptSHA256":transcript_sha256,"TranscriptRevision":format!("sha256:{transcript_sha256}"),"SnapshotJSON":serde_json::to_string(&snapshot).unwrap(),"ChunkCount":planned.user_prompts.len()}));
        prompts.push(json!({"Input":{"Job":{"job":job,"transcriptSegments":job.transcript_segments,"speakerSegments":job.speaker_segments},"Template":template,"IncludeSpeaker":input.include_speaker,"IncludeTimestamp":input.include_timestamp,"UseMembers":input.use_member_mapping,"Members":members,"Extra":input.extra_instructions},"Prompt":{"System":built.system,"User":built.user},"Plan":{"FallbackTitle":planned.fallback_title,"RequiredSpeakers":planned.required_speakers,"UserPrompts":planned.user_prompts}}));
    }
    let first = json!({"title":"模型标题","overview":" 摘要\n第二行 ","topics":["议题",2],"decisions":[],"actionItems":[{"task":" 跟进 ","owner":"甲","dueDate":3},{"task":""}],"risks":[],"followUps":[],"speakerReports":[{"speakerLabel":" 甲 ","department":"","weeklySummary":["内容",1],"nextWeekPlan":[],"summary":[]}],"globalSummary":[" 全局 "]});
    let second = json!({"title":"第二标题","overview":"摘要","topics":["议题",3],"decisions":null,"actionItems":[],"risks":{"b":"乙","a":true},"followUps":false,"speakerReports":[{"resolvedName":"甲","department":"研发","weeklySummary":["内容",2],"summary":["结论"]},{"name":"乙","department":"测试","summary":["内容"]}],"globalSummary":["全局"]});
    let mut responses = Vec::new();
    for (content, reason, parts) in [
        (first.clone(), Some("stop"), false),
        (second.clone(), Some("stop"), true),
        (first.clone(), Some("length"), false),
        (first.clone(), None, false),
        (json!({}), Some("stop"), false),
        (
            json!({"title":null,"overview":null,"topics":null,"decisions":null,"actionItems":null,"risks":null,"followUps":null,"speakerReports":null,"globalSummary":null}),
            Some("stop"),
            false,
        ),
    ] {
        let raw = envelope(content, reason, parts);
        let result = response::parse_ai_summary_result(&raw, "回退标题");
        responses.push(json!({"Raw":raw,"Valid":result.is_ok(),"Expected":result.ok()}));
    }
    let raws = vec![
        envelope(first, Some("stop"), false),
        envelope(second, Some("stop"), true),
    ];
    let required = vec!["甲".into(), "乙".into(), "丙".into()];
    let merged = response::merge_ai_summary_responses(&raws, "回退标题", &required).unwrap();
    let expected = response::parse_ai_summary_result(&merged, "回退标题").unwrap();
    let structured = response::parse_ai_summary_structured(&merged).unwrap();
    let data = json!({"Executions":executions,"Prompts":prompts,"Responses":responses,"Merge":{"Raw":raws,"Required":required,"Expected":expected,"Structured":structured}});
    std::fs::write(
        directory.join("ai-expected.json"),
        serde_json::to_vec(&data).unwrap(),
    )
    .unwrap();
}
