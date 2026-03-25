mod local_jobs;

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        .plugin(tauri_plugin_dialog::init())
        .plugin(tauri_plugin_fs::init())
        .invoke_handler(tauri::generate_handler![
            local_jobs::create_job,
            local_jobs::delete_job,
            local_jobs::get_job,
            local_jobs::get_job_result,
            local_jobs::list_jobs,
            local_jobs::retry_job
        ])
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}
