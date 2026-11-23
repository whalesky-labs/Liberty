// Transitional consumer of the shared production runtime inputs. This module
// leaves with the old desktop client; the recipe and pins remain shared.
use serde::Deserialize;
use sha2::{Digest, Sha256};
use std::{
    env, fs,
    io::Write,
    path::{Path, PathBuf},
    process::Command,
    sync::atomic::{AtomicU64, Ordering},
    time::Duration,
};

use super::{
    archive::download_remote_asset,
    manifest::{load_manifest, RuntimeInputFile, RuntimeManifest},
    process::run_command_with_log_timeout,
};
use crate::local_db::LocalResult;

const BUILDER: &[u8] =
    include_bytes!("../../../../../python/funasr-runner/tools/build_vendor_wheel.py");
const RECIPE: &[u8] =
    include_bytes!("../../../../../python/funasr-runner/vendor/funasr-1.3.9.json");
const VENDOR_FILES: [&str; 5] = [
    "funasr-1.3.9+liberty.local2-py3-none-any.whl",
    "METADATA.patch",
    "recipe.json",
    "provenance.json",
    "source.cdx.json",
];
const LOCK_FILES: [&str; 3] = [
    "requirements-lock-darwin-aarch64.txt",
    "requirements-lock-darwin-x64.txt",
    "requirements-lock-windows-x64.txt",
];
static WORKSPACE_SEQUENCE: AtomicU64 = AtomicU64::new(0);

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
pub(super) struct VendorRecipe {
    schema_version: u32,
    pub upstream: UpstreamWheel,
    output: WheelIdentity,
}

#[derive(Deserialize)]
pub(super) struct UpstreamWheel {
    name: String,
    version: String,
    pub filename: String,
    pub url: String,
    pub sha256: String,
    bytes: u64,
    purl: String,
}

#[derive(Deserialize)]
struct WheelIdentity {
    filename: String,
    version: String,
    sha256: String,
    bytes: u64,
}

fn input_pin<'a>(manifest: &'a RuntimeManifest, name: &str) -> LocalResult<&'a RuntimeInputFile> {
    let inputs = manifest
        .production_inputs
        .as_ref()
        .ok_or_else(|| "生产运行时缺少固定构建输入。".to_string())?;
    let mut names: Vec<_> = inputs
        .files
        .iter()
        .map(|pin| pin.file_name.as_str())
        .collect();
    names.sort_unstable();
    let mut expected: Vec<_> = VENDOR_FILES.into_iter().chain(LOCK_FILES).collect();
    expected.sort_unstable();
    if names != expected
        || inputs.builder.file_name != "build_vendor_wheel.py"
        || inputs.builder.bytes == 0
        || inputs.builder.bytes > 128 * 1024
        || inputs
            .files
            .iter()
            .any(|pin| pin.bytes == 0 || pin.bytes > 2 * 1024 * 1024)
    {
        return Err("生产运行时构建输入集合无效。".into());
    }
    inputs
        .files
        .iter()
        .find(|pin| pin.file_name == name)
        .ok_or_else(|| "生产运行时输入没有固定摘要。".into())
}

fn verify_bytes(bytes: &[u8], pin: &RuntimeInputFile) -> LocalResult<()> {
    if bytes.len() as u64 != pin.bytes || format!("{:x}", Sha256::digest(bytes)) != pin.sha256 {
        return Err(format!("生产运行时输入校验失败：{}", pin.file_name));
    }
    Ok(())
}

pub(super) fn production_recipe(manifest: &RuntimeManifest) -> LocalResult<VendorRecipe> {
    verify_bytes(RECIPE, input_pin(manifest, "recipe.json")?)?;
    let recipe: VendorRecipe = serde_json::from_slice(RECIPE).map_err(|err| err.to_string())?;
    let output = input_pin(manifest, &recipe.output.filename)?;
    if recipe.schema_version != 1
        || recipe.upstream.name != "funasr"
        || recipe.upstream.version != "1.3.9"
        || recipe.upstream.filename != "funasr-1.3.9-py3-none-any.whl"
        || recipe.upstream.purl != "pkg:pypi/funasr@1.3.9"
        || !recipe
            .upstream
            .url
            .starts_with("https://files.pythonhosted.org/packages/")
        || recipe.upstream.bytes == 0
        || recipe.upstream.bytes > 2 * 1024 * 1024
        || recipe.output.version != "1.3.9+liberty.local2"
        || recipe.output.filename != VENDOR_FILES[0]
        || recipe.output.bytes != output.bytes
        || recipe.output.sha256 != output.sha256
    {
        return Err("生产运行时配方身份无效。".into());
    }
    Ok(recipe)
}

fn embedded_lock(platform: &str) -> LocalResult<(&'static str, &'static [u8])> {
    match platform {
        "darwin-aarch64" => Ok((
            LOCK_FILES[0],
            include_bytes!(
                "../../../../../python/funasr-runner/requirements-lock-darwin-aarch64.txt"
            ),
        )),
        "darwin-x64" => Ok((
            LOCK_FILES[1],
            include_bytes!("../../../../../python/funasr-runner/requirements-lock-darwin-x64.txt"),
        )),
        "windows-x64" => Ok((
            LOCK_FILES[2],
            include_bytes!("../../../../../python/funasr-runner/requirements-lock-windows-x64.txt"),
        )),
        _ => Err("当前平台没有经过验证的生产 Python 依赖锁。".into()),
    }
}

pub(super) fn verify_lock_file(path: &Path, platform: &str) -> LocalResult<()> {
    let manifest = load_manifest()?;
    let (name, embedded) = embedded_lock(platform)?;
    let pin = input_pin(&manifest, name)?;
    verify_bytes(embedded, pin)?;
    verify_file(path, pin)
}

fn verify_file(path: &Path, pin: &RuntimeInputFile) -> LocalResult<()> {
    let metadata = fs::symlink_metadata(path).map_err(|err| err.to_string())?;
    if !metadata.is_file() || metadata.len() != pin.bytes {
        return Err(format!("生产运行时输入文件无效：{}", pin.file_name));
    }
    verify_bytes(&fs::read(path).map_err(|err| err.to_string())?, pin)
}

fn write_new(path: &Path, bytes: &[u8]) -> LocalResult<()> {
    let mut file = fs::OpenOptions::new()
        .write(true)
        .create_new(true)
        .open(path)
        .map_err(|err| err.to_string())?;
    file.write_all(bytes).map_err(|err| err.to_string())
}

pub(super) struct ProductionDependencies {
    root: PathBuf,
    lock: PathBuf,
}

impl Drop for ProductionDependencies {
    fn drop(&mut self) {
        let _ = fs::remove_dir_all(&self.root);
    }
}

impl ProductionDependencies {
    fn new(parent: &Path, lock_name: &str) -> LocalResult<Self> {
        for _ in 0..100 {
            let sequence = WORKSPACE_SEQUENCE.fetch_add(1, Ordering::Relaxed);
            let root = parent.join(format!(".python-vendor-{}-{sequence}", std::process::id()));
            let mut builder = fs::DirBuilder::new();
            #[cfg(unix)]
            {
                use std::os::unix::fs::DirBuilderExt;
                builder.mode(0o700);
            }
            match builder.create(&root) {
                Ok(()) => {
                    return Ok(Self {
                        lock: root.join(lock_name),
                        root,
                    })
                }
                Err(error) if error.kind() == std::io::ErrorKind::AlreadyExists => continue,
                Err(error) => return Err(error.to_string()),
            }
        }
        Err("无法创建独立的生产运行时输入目录。".into())
    }

    pub fn install_command(&self, python: &Path, index_url: &str, backend: &str) -> Command {
        let mut command = Command::new(python);
        scrub_python_environment(&mut command, env::vars_os().map(|(name, _)| name));
        command
            .env("PYTHONUTF8", "1")
            .env("LIBERTY_ASR_BACKEND", backend)
            .arg("-I")
            .args([
                "-m",
                "pip",
                "--isolated",
                "install",
                "--disable-pip-version-check",
                "--no-input",
                "--no-cache-dir",
                "--require-hashes",
                "--no-build-isolation",
            ])
            .arg("--find-links")
            .arg(self.root.join("output"))
            .arg("--index-url")
            .arg(index_url)
            .arg("-r")
            .arg(&self.lock);
        command
    }
}

fn scrub_python_environment(
    command: &mut Command,
    names: impl Iterator<Item = std::ffi::OsString>,
) {
    for name in names {
        let upper = name.to_string_lossy().to_ascii_uppercase();
        if upper.starts_with("PYTHON") || upper.starts_with("PIP_") {
            command.env_remove(name);
        }
    }
    command.env(
        "PIP_CONFIG_FILE",
        if cfg!(windows) { "NUL" } else { "/dev/null" },
    );
}

fn verify_outputs(directory: &Path, manifest: &RuntimeManifest) -> LocalResult<()> {
    let mut names = Vec::new();
    for entry in fs::read_dir(directory).map_err(|err| err.to_string())? {
        let entry = entry.map_err(|err| err.to_string())?;
        names.push(
            entry
                .file_name()
                .into_string()
                .map_err(|_| "生产 vendor 文件名无效。".to_string())?,
        );
    }
    names.sort_unstable();
    let mut expected = VENDOR_FILES.map(str::to_owned);
    expected.sort_unstable();
    if names != expected {
        return Err("生产 vendor 输出集合与固定配方不一致。".into());
    }
    for name in VENDOR_FILES {
        verify_file(&directory.join(name), input_pin(manifest, name)?)?;
    }
    Ok(())
}

pub(super) fn prepare_production_dependencies(
    python: &Path,
    platform: &str,
    log: &Path,
) -> LocalResult<ProductionDependencies> {
    let manifest = load_manifest()?;
    let recipe = production_recipe(&manifest)?;
    let inputs = manifest
        .production_inputs
        .as_ref()
        .ok_or_else(|| "生产构建输入缺失。".to_string())?;
    verify_bytes(BUILDER, &inputs.builder)?;
    let (lock_name, lock) = embedded_lock(platform)?;
    verify_bytes(lock, input_pin(&manifest, lock_name)?)?;
    let parent = log
        .parent()
        .ok_or_else(|| "运行时日志路径无父目录。".to_string())?;
    let workspace = ProductionDependencies::new(parent, lock_name)?;
    fs::create_dir(workspace.root.join("tools")).map_err(|err| err.to_string())?;
    fs::create_dir(workspace.root.join("vendor")).map_err(|err| err.to_string())?;
    let builder = workspace.root.join("tools/build_vendor_wheel.py");
    write_new(&builder, BUILDER)?;
    write_new(&workspace.root.join("vendor/funasr-1.3.9.json"), RECIPE)?;
    write_new(&workspace.lock, lock)?;
    let source = workspace.root.join(&recipe.upstream.filename);
    download_remote_asset(
        &recipe.upstream.url,
        &source,
        log,
        "Downloading pinned upstream FunASR wheel",
    )?;
    verify_file(
        &source,
        &RuntimeInputFile {
            file_name: recipe.upstream.filename,
            bytes: recipe.upstream.bytes,
            sha256: recipe.upstream.sha256,
        },
    )?;
    let output = workspace.root.join("output");
    let mut command = Command::new(python);
    scrub_python_environment(&mut command, env::vars_os().map(|(name, _)| name));
    command
        .args(["-I", "-S"])
        .arg(builder)
        .arg("--upstream-wheel")
        .arg(source)
        .arg("--output-dir")
        .arg(&output);
    run_command_with_log_timeout(
        &mut command,
        log,
        "Building pinned FunASR vendor wheel",
        Duration::from_secs(60),
    )?;
    verify_outputs(&output, &manifest)?;
    Ok(workspace)
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::ffi::{OsStr, OsString};

    fn workspace() -> ProductionDependencies {
        ProductionDependencies::new(&env::temp_dir(), "lock.txt").expect("private workspace")
    }

    #[test]
    fn production_embedded_sources_and_all_locks_match_shared_pins() {
        let manifest = load_manifest().expect("manifest");
        let recipe = production_recipe(&manifest).expect("fixed recipe");
        assert_eq!(recipe.upstream.version, "1.3.9");
        verify_bytes(
            BUILDER,
            &manifest.production_inputs.as_ref().unwrap().builder,
        )
        .unwrap();
        for platform in ["darwin-aarch64", "darwin-x64", "windows-x64"] {
            let (name, content) = embedded_lock(platform).unwrap();
            verify_bytes(content, input_pin(&manifest, name).unwrap()).unwrap();
        }
        assert!(embedded_lock("windows-x86").is_err());
    }

    #[test]
    fn production_recipe_rejects_missing_inputs_changed_pins_and_duplicate_names() {
        for mutation in 0..4 {
            let mut manifest = load_manifest().unwrap();
            match mutation {
                0 => manifest.production_inputs = None,
                1 => {
                    manifest
                        .production_inputs
                        .as_mut()
                        .unwrap()
                        .files
                        .iter_mut()
                        .find(|pin| pin.file_name == "recipe.json")
                        .unwrap()
                        .sha256 = "a".repeat(64)
                }
                2 => {
                    manifest.production_inputs.as_mut().unwrap().files[0].file_name =
                        "../outside".into()
                }
                _ => {
                    manifest.production_inputs.as_mut().unwrap().files[0].file_name =
                        VENDOR_FILES[0].into()
                }
            }
            assert!(production_recipe(&manifest).is_err());
        }
    }

    #[test]
    fn upstream_download_requires_both_fixed_recipe_name_and_url() {
        let recipe = production_recipe(&load_manifest().unwrap()).unwrap();
        assert_eq!(
            super::super::manifest::expected_asset_sha256(
                &recipe.upstream.filename,
                &recipe.upstream.url
            )
            .unwrap(),
            recipe.upstream.sha256
        );
        assert!(
            super::super::manifest::expected_asset_sha256("other.whl", &recipe.upstream.url)
                .is_err()
        );
        assert!(super::super::manifest::expected_asset_sha256(
            &recipe.upstream.filename,
            "https://files.pythonhosted.org/packages/other.whl"
        )
        .is_err());
    }

    #[test]
    fn vendor_outputs_require_exact_set_sizes_hashes_and_plain_files() {
        let workspace = workspace();
        let output = workspace.root.join("output");
        fs::create_dir(&output).unwrap();
        let mut manifest = load_manifest().unwrap();
        for name in VENDOR_FILES {
            let content = name.as_bytes();
            write_new(&output.join(name), content).unwrap();
            let pin = manifest
                .production_inputs
                .as_mut()
                .unwrap()
                .files
                .iter_mut()
                .find(|pin| pin.file_name == name)
                .unwrap();
            pin.bytes = content.len() as u64;
            pin.sha256 = format!("{:x}", Sha256::digest(content));
        }
        verify_outputs(&output, &manifest).unwrap();
        write_new(&output.join("extra"), b"extra").unwrap();
        assert!(verify_outputs(&output, &manifest).is_err());
        fs::remove_file(output.join("extra")).unwrap();
        fs::write(output.join(VENDOR_FILES[0]), b"corrupt").unwrap();
        assert!(verify_outputs(&output, &manifest).is_err());
        fs::remove_file(output.join(VENDOR_FILES[0])).unwrap();
        fs::create_dir(output.join(VENDOR_FILES[0])).unwrap();
        assert!(verify_outputs(&output, &manifest).is_err());
    }

    #[cfg(unix)]
    #[test]
    fn linked_outputs_are_rejected_and_workspace_is_private() {
        use std::os::unix::{fs::symlink, fs::PermissionsExt};
        let workspace = workspace();
        assert_eq!(
            fs::metadata(&workspace.root).unwrap().permissions().mode() & 0o777,
            0o700
        );
        let target = workspace.root.join("target");
        write_new(&target, b"fixed").unwrap();
        let link = workspace.root.join("link");
        symlink(target, &link).unwrap();
        let pin = RuntimeInputFile {
            file_name: "link".into(),
            bytes: 5,
            sha256: format!("{:x}", Sha256::digest(b"fixed")),
        };
        assert!(verify_file(&link, &pin).is_err());
    }

    #[test]
    fn pip_uses_private_verified_inputs_and_explicit_isolation() {
        let workspace = workspace();
        let command =
            workspace.install_command(Path::new("python"), "https://pypi.org/simple", "funasr");
        let args: Vec<_> = command.get_args().collect();
        assert_eq!(&args[..5], ["-I", "-m", "pip", "--isolated", "install"]);
        for flag in [
            "--require-hashes",
            "--no-build-isolation",
            "--no-cache-dir",
            "--find-links",
        ] {
            assert!(args.contains(&OsStr::new(flag)));
        }
        assert_eq!(args.last().copied(), Some(workspace.lock.as_os_str()));
        let mut scrubbed = Command::new("python");
        scrub_python_environment(
            &mut scrubbed,
            ["PIP_EXTRA_INDEX_URL", "PYTHONPATH", "pip_target"]
                .into_iter()
                .map(OsString::from),
        );
        let removed: Vec<_> = scrubbed
            .get_envs()
            .filter(|(_, value)| value.is_none())
            .map(|(name, _)| name)
            .collect();
        for name in ["PIP_EXTRA_INDEX_URL", "PYTHONPATH", "pip_target"] {
            assert!(removed.contains(&OsStr::new(name)));
        }
    }

    #[test]
    fn private_workspace_rejects_file_collisions_and_cleans_its_own_outputs() {
        let workspace = workspace();
        let root = workspace.root.clone();
        write_new(&workspace.lock, b"fixed").unwrap();
        assert!(write_new(&workspace.lock, b"replacement").is_err());
        assert_eq!(fs::read(&workspace.lock).unwrap(), b"fixed");
        drop(workspace);
        assert!(!root.exists());
    }

    #[test]
    fn managed_validation_rejects_a_changed_lock_resource() {
        let workspace = workspace();
        let (_, lock) = embedded_lock("darwin-x64").unwrap();
        write_new(&workspace.lock, lock).unwrap();
        verify_lock_file(&workspace.lock, "darwin-x64").unwrap();
        fs::write(&workspace.lock, b"unverified==1.0\n").unwrap();
        assert!(verify_lock_file(&workspace.lock, "darwin-x64").is_err());
    }
}
