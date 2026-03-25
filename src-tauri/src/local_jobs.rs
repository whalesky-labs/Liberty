use serde::{Deserialize, Serialize};
use std::{
    fs::{self, OpenOptions},
    io::Write,
    path::{Path, PathBuf},
    process::Command,
    sync::atomic::{AtomicU64, Ordering},
    time::{SystemTime, UNIX_EPOCH},
};
use tauri::{AppHandle, Manager};

static JOB_COUNTER: AtomicU64 = AtomicU64::new(1);

type LocalResult<T> = Result<T, String>;

#[derive(Debug, Serialize, Deserialize, Clone)]
#[serde(rename_all = "camelCase")]
pub struct TranscriptSegment {
    pub id: String,
    pub start_ms: u64,
    pub end_ms: u64,
    pub speaker: Option<String>,
    pub text: String,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
#[serde(rename_all = "camelCase")]
pub struct MeetingSummary {
    pub overview: String,
    pub topics: Vec<String>,
    pub decisions: Vec<String>,
    pub action_items: Vec<String>,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
#[serde(rename_all = "camelCase")]
pub struct MeetingSourceFile {
    pub id: String,
    pub name: String,
    pub path: Option<String>,
    pub size_label: String,
    pub kind: String,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
#[serde(rename_all = "camelCase")]
pub struct MeetingJob {
    pub id: String,
    pub title: String,
    pub source_files: Vec<MeetingSourceFile>,
    pub duration_minutes: u32,
    pub created_at: String,
    pub hotwords: Vec<String>,
    pub lang: String,
    pub enable_speaker: bool,
    pub summary_template: String,
    pub upload_status: String,
    pub asr_status: String,
    pub summary_status: String,
    pub overall_status: String,
    pub failure_reason: Option<String>,
    pub transcript_segments: Vec<TranscriptSegment>,
    pub speaker_segments: Vec<TranscriptSegment>,
    pub summary: MeetingSummary,
    pub export_formats: Vec<String>,
    pub last_exported_at: Option<String>,
    pub process_log: Option<String>,
    pub python_path: Option<String>,
    pub runner_script_path: Option<String>,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
#[serde(rename_all = "camelCase")]
pub struct CreateJobInput {
    pub title: String,
    pub files: Vec<MeetingSourceFile>,
    pub hotwords: Vec<String>,
    pub lang: String,
    pub enable_speaker: bool,
    pub summary_template: String,
    pub created_at: String,
    pub python_path: String,
    pub runner_script_path: String,
}

#[derive(Debug, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
struct ProgressSnapshot {
    stage: String,
    status_message: Option<String>,
    failure_reason: Option<String>,
    updated_at: String,
}

#[derive(Debug, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
struct RunnerResult {
    duration_minutes: Option<u32>,
    transcript_segments: Option<Vec<TranscriptSegment>>,
    speaker_segments: Option<Vec<TranscriptSegment>>,
    failure_reason: Option<String>,
}

#[tauri::command]
pub fn list_jobs(app: AppHandle) -> LocalResult<Vec<MeetingJob>> {
    let root = jobs_root(&app)?;

    if !root.exists() {
        return Ok(Vec::new());
    }

    let mut jobs = Vec::new();

    for entry in fs::read_dir(root).map_err(|err| err.to_string())? {
        let entry = entry.map_err(|err| err.to_string())?;
        let path = entry.path();

        if !path.is_dir() {
            continue;
        }

        if let Ok(job) = read_materialized_job(&path) {
            jobs.push(job);
        }
    }

    jobs.sort_by(|left, right| right.created_at.cmp(&left.created_at));
    Ok(jobs)
}

#[tauri::command]
pub fn get_job(app: AppHandle, id: String) -> LocalResult<MeetingJob> {
    let job_dir = job_dir(&app, &id)?;
    read_materialized_job(&job_dir)
}

#[tauri::command]
pub fn get_job_result(app: AppHandle, id: String) -> LocalResult<MeetingJob> {
    let job_dir = job_dir(&app, &id)?;
    read_materialized_job(&job_dir)
}

#[tauri::command]
pub fn delete_job(app: AppHandle, id: String) -> LocalResult<()> {
    let job_dir = job_dir(&app, &id)?;

    if !job_dir.exists() {
        return Ok(());
    }

    fs::remove_dir_all(job_dir).map_err(|err| err.to_string())
}

#[tauri::command]
pub fn create_job(app: AppHandle, input: CreateJobInput) -> LocalResult<MeetingJob> {
    validate_create_input(&input)?;

    let job = build_initial_job(input);
    let job_dir = job_dir(&app, &job.id)?;

    fs::create_dir_all(&job_dir).map_err(|err| err.to_string())?;
    reset_process_log(&job_dir)?;
    write_job(&job_dir, &job)?;
    write_progress(&job_dir, "queued", Some("任务已创建，等待本地处理。"), None)?;
    spawn_local_job(app, job.id.clone());

    read_materialized_job(&job_dir)
}

#[tauri::command]
pub fn retry_job(
    app: AppHandle,
    id: String,
    python_path: String,
    runner_script_path: String,
) -> LocalResult<MeetingJob> {
    if python_path.trim().is_empty() {
        return Err("请先配置 Python 可执行文件路径。".into());
    }

    if runner_script_path.trim().is_empty() {
        return Err("请先配置 Runner 脚本路径。".into());
    }

    let job_dir = job_dir(&app, &id)?;
    let mut job = read_job(&job_dir)?;
    let first_file = job
        .source_files
        .first()
        .ok_or_else(|| "任务缺少输入文件。".to_string())?;

    if first_file.path.as_deref().unwrap_or("").trim().is_empty() {
        return Err("本地模式只支持带本地路径的文件。".into());
    }

    job.python_path = Some(python_path);
    job.runner_script_path = Some(runner_script_path);
    job.upload_status = "uploaded".into();
    job.asr_status = "queued".into();
    job.summary_status = "queued".into();
    job.overall_status = "queued".into();
    job.failure_reason = None;
    job.transcript_segments.clear();
    job.speaker_segments.clear();
    job.summary = empty_summary();
    reset_process_log(&job_dir)?;
    write_job(&job_dir, &job)?;
    write_progress(&job_dir, "queued", Some("任务已重新排队，准备执行。"), None)?;
    spawn_local_job(app, id.clone());

    read_materialized_job(&job_dir)
}

fn spawn_local_job(app: AppHandle, job_id: String) {
    std::thread::spawn(move || {
        if let Err(error) = execute_local_job(&app, &job_id) {
            let _ = mark_failed(&app, &job_id, &error);
        }
    });
}

fn execute_local_job(app: &AppHandle, job_id: &str) -> LocalResult<()> {
    let job_dir = job_dir(app, job_id)?;
    let mut job = read_job(&job_dir)?;
    let input_file = job
        .source_files
        .first()
        .and_then(|file| file.path.clone())
        .ok_or_else(|| "任务缺少可处理的本地文件路径。".to_string())?;
    let python_path = job
        .python_path
        .clone()
        .ok_or_else(|| "未找到 Python 可执行文件路径。".to_string())?;
    let runner_script_path = job
        .runner_script_path
        .clone()
        .ok_or_else(|| "未找到 Runner 脚本路径。".to_string())?;

    job.overall_status = "transcribing".into();
    job.asr_status = "transcribing".into();
    write_job(&job_dir, &job)?;
    write_progress(&job_dir, "transcribing", Some("正在调用本地 Python 处理。"), None)?;

    let output = Command::new(&python_path)
        .env("OMP_NUM_THREADS", "1")
        .env("KMP_DUPLICATE_LIB_OK", "TRUE")
        .arg(&runner_script_path)
        .arg("--job-dir")
        .arg(&job_dir)
        .arg("--input")
        .arg(&input_file)
        .arg("--lang")
        .arg(&job.lang)
        .arg("--speaker")
        .arg(if job.enable_speaker { "true" } else { "false" })
        .arg("--hotwords")
        .arg(job.hotwords.join(","))
        .output()
        .map_err(|err| format!("无法启动本地 Python 处理进程: {err}"))?;

    append_process_log(&job_dir, &output.stdout)?;
    append_process_log(&job_dir, &output.stderr)?;

    if !output.status.success() {
        let code = output.status.code().unwrap_or(-1);
        let detailed =
            summarize_process_log(&job_dir).unwrap_or_else(|| format!("本地 Python 处理失败，退出码 {code}。"));
        return Err(detailed);
    }

    let runner_result = read_runner_result(&job_dir)?;
    if let Some(reason) = runner_result.failure_reason.clone() {
        return Err(reason);
    }

    if job.enable_speaker && runner_result.speaker_segments.as_ref().is_none_or(Vec::is_empty) {
        return Err("Runner 未返回说话人分离结果。".into());
    }

    job.duration_minutes = runner_result.duration_minutes.unwrap_or(job.duration_minutes);
    job.transcript_segments = runner_result.transcript_segments.unwrap_or_default();
    job.speaker_segments = runner_result
        .speaker_segments
        .unwrap_or_else(|| job.transcript_segments.clone());
    job.summary = empty_summary();
    job.summary_status = "completed".into();
    job.asr_status = "completed".into();
    job.overall_status = "completed".into();
    job.failure_reason = None;
    write_job(&job_dir, &job)?;
    write_progress(&job_dir, "completed", Some("本地处理已完成。"), None)?;

    Ok(())
}

fn build_initial_job(input: CreateJobInput) -> MeetingJob {
    MeetingJob {
        id: make_job_id(),
        title: input.title,
        source_files: input.files,
        duration_minutes: 0,
        created_at: input.created_at,
        hotwords: input.hotwords,
        lang: input.lang,
        enable_speaker: input.enable_speaker,
        summary_template: input.summary_template,
        upload_status: "uploaded".into(),
        asr_status: "queued".into(),
        summary_status: "queued".into(),
        overall_status: "queued".into(),
        failure_reason: None,
        transcript_segments: Vec::new(),
        speaker_segments: Vec::new(),
        summary: empty_summary(),
        export_formats: vec!["txt".into(), "md".into(), "srt".into(), "docx".into()],
        last_exported_at: None,
        process_log: None,
        python_path: Some(input.python_path),
        runner_script_path: Some(input.runner_script_path),
    }
}

fn validate_create_input(input: &CreateJobInput) -> LocalResult<()> {
    if input.title.trim().is_empty() {
        return Err("任务标题不能为空。".into());
    }

    if input.files.len() != 1 {
        return Err("本地 FunASR 模式当前只支持单文件任务。".into());
    }

    if input.python_path.trim().is_empty() {
        return Err("请先配置 Python 可执行文件路径。".into());
    }

    if input.runner_script_path.trim().is_empty() {
        return Err("请先配置 Runner 脚本路径。".into());
    }

    let file = input.files.first().ok_or_else(|| "请选择一个输入文件。".to_string())?;
    let file_path = file
        .path
        .as_deref()
        .ok_or_else(|| "本地模式只支持带本地路径的文件。".to_string())?;

    if !Path::new(file_path).exists() {
        return Err("输入文件不存在或当前路径不可访问。".into());
    }

    Ok(())
}

fn read_materialized_job(job_dir: &Path) -> LocalResult<MeetingJob> {
    let mut job = read_job(job_dir)?;
    job.process_log = read_process_log(job_dir).ok();

    if let Ok(progress) = read_progress(job_dir) {
        apply_progress_snapshot(&mut job, &progress);
    }

    Ok(job)
}

fn read_job(job_dir: &Path) -> LocalResult<MeetingJob> {
    read_json(&job_dir.join("job.json"))
}

fn read_progress(job_dir: &Path) -> LocalResult<ProgressSnapshot> {
    read_json(&job_dir.join("progress.json"))
}

fn read_runner_result(job_dir: &Path) -> LocalResult<RunnerResult> {
    read_json(&job_dir.join("result.json"))
}

fn write_job(job_dir: &Path, job: &MeetingJob) -> LocalResult<()> {
    write_json(&job_dir.join("job.json"), job)
}

fn write_progress(
    job_dir: &Path,
    stage: &str,
    status_message: Option<&str>,
    failure_reason: Option<&str>,
) -> LocalResult<()> {
    write_json(
        &job_dir.join("progress.json"),
        &ProgressSnapshot {
            stage: stage.into(),
            status_message: status_message.map(ToOwned::to_owned),
            failure_reason: failure_reason.map(ToOwned::to_owned),
            updated_at: now_iso_like(),
        },
    )
}

fn append_process_log(job_dir: &Path, bytes: &[u8]) -> LocalResult<()> {
    if bytes.is_empty() {
        return Ok(());
    }

    let log_path = job_dir.join("process.log");
    let mut file = OpenOptions::new()
        .create(true)
        .append(true)
        .open(log_path)
        .map_err(|err| err.to_string())?;
    file.write_all(bytes).map_err(|err| err.to_string())
}

fn reset_process_log(job_dir: &Path) -> LocalResult<()> {
    fs::write(job_dir.join("process.log"), []).map_err(|err| err.to_string())
}

fn mark_failed(app: &AppHandle, job_id: &str, reason: &str) -> LocalResult<()> {
    let job_dir = job_dir(app, job_id)?;
    let mut job = read_job(&job_dir)?;
    let detailed_reason = summarize_process_log(&job_dir).unwrap_or_else(|| reason.to_string());
    job.asr_status = "failed".into();
    job.overall_status = "failed".into();
    job.failure_reason = Some(detailed_reason.clone());
    job.process_log = read_process_log(&job_dir).ok();
    write_job(&job_dir, &job)?;
    write_progress(&job_dir, "failed", Some(&detailed_reason), Some(&detailed_reason))
}

fn apply_progress_snapshot(job: &mut MeetingJob, progress: &ProgressSnapshot) {
    match progress.stage.as_str() {
        "queued" => {
            job.upload_status = "uploaded".into();
            job.asr_status = "queued".into();
            job.summary_status = "queued".into();
            job.overall_status = "queued".into();
        }
        "transcribing" => {
            job.upload_status = "uploaded".into();
            job.asr_status = "transcribing".into();
            job.summary_status = "queued".into();
            job.overall_status = "transcribing".into();
        }
        "speaker_processing" => {
            job.upload_status = "uploaded".into();
            job.asr_status = "speaker_processing".into();
            job.summary_status = "queued".into();
            job.overall_status = "speaker_processing".into();
        }
        "completed" => {
            job.upload_status = "uploaded".into();
            job.asr_status = "completed".into();
            job.summary_status = "completed".into();
            job.overall_status = "completed".into();
        }
        "failed" => {
            job.asr_status = "failed".into();
            job.overall_status = "failed".into();
        }
        _ => {}
    }

    if let Some(reason) = progress
        .failure_reason
        .clone()
        .or_else(|| progress.status_message.clone())
    {
        if progress.stage == "failed" {
            job.failure_reason = Some(reason);
        }
    }
}

fn jobs_root(app: &AppHandle) -> LocalResult<PathBuf> {
    let root = app
        .path()
        .app_local_data_dir()
        .map_err(|err| err.to_string())?
        .join("jobs");
    fs::create_dir_all(&root).map_err(|err| err.to_string())?;
    Ok(root)
}

fn job_dir(app: &AppHandle, job_id: &str) -> LocalResult<PathBuf> {
    Ok(jobs_root(app)?.join(job_id))
}

fn write_json<T: Serialize>(path: &Path, value: &T) -> LocalResult<()> {
    let bytes = serde_json::to_vec_pretty(value).map_err(|err| err.to_string())?;
    fs::write(path, bytes).map_err(|err| err.to_string())
}

fn read_json<T: for<'de> Deserialize<'de>>(path: &Path) -> LocalResult<T> {
    let bytes = fs::read(path).map_err(|err| err.to_string())?;
    serde_json::from_slice(&bytes).map_err(|err| err.to_string())
}

fn read_process_log(job_dir: &Path) -> LocalResult<String> {
    let content = fs::read_to_string(job_dir.join("process.log")).map_err(|err| err.to_string())?;
    let trimmed = content.trim().to_string();

    if trimmed.is_empty() {
        return Err("日志为空".into());
    }

    Ok(trimmed)
}

fn summarize_process_log(job_dir: &Path) -> Option<String> {
    let log = read_process_log(job_dir).ok()?;
    let lines: Vec<&str> = log.lines().filter(|line| !line.trim().is_empty()).collect();

    if lines.is_empty() {
        return None;
    }

    let tail = lines.iter().rev().take(3).copied().collect::<Vec<_>>();
    Some(tail.into_iter().rev().collect::<Vec<_>>().join("\n"))
}

fn empty_summary() -> MeetingSummary {
    MeetingSummary {
        overview: String::new(),
        topics: Vec::new(),
        decisions: Vec::new(),
        action_items: Vec::new(),
    }
}

fn make_job_id() -> String {
    format!(
        "job-{}-{}",
        unix_timestamp_millis(),
        JOB_COUNTER.fetch_add(1, Ordering::Relaxed)
    )
}

fn now_iso_like() -> String {
    format!("{}", unix_timestamp_millis())
}

fn unix_timestamp_millis() -> u128 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|duration| duration.as_millis())
        .unwrap_or_default()
}
