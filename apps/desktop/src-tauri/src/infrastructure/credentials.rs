#[cfg(target_os = "macos")]
use std::{
    io::Write,
    process::{Command, Stdio},
};

use crate::domain::error::AppError;
use crate::infrastructure::ids;

pub type CredentialResult<T> = Result<T, AppError>;

#[cfg(any(target_os = "macos", windows))]
const SERVICE_NAME: &str = "LibertyDesktop";
#[cfg(target_os = "macos")]
const MACOS_SECURITY_PATH: &str = "/usr/bin/security";

pub trait CredentialStore {
    fn get_secret(&self, key: &str) -> CredentialResult<Option<String>>;
    fn set_secret(&self, key: &str, value: &str) -> CredentialResult<()>;
    fn delete_secret(&self, key: &str) -> CredentialResult<()>;
}

#[derive(Debug, Default)]
pub struct SystemCredentialStore;

impl CredentialStore for SystemCredentialStore {
    fn get_secret(&self, key: &str) -> CredentialResult<Option<String>> {
        get_system_secret(key)
    }

    fn set_secret(&self, key: &str, value: &str) -> CredentialResult<()> {
        set_system_secret(key, value)
    }

    fn delete_secret(&self, key: &str) -> CredentialResult<()> {
        delete_system_secret(key)
    }
}

pub fn default_credential_store() -> SystemCredentialStore {
    SystemCredentialStore
}

pub fn credential_key_for_ai_model(model_id: &str) -> String {
    format!("ai-model:{model_id}:api-key")
}

pub struct CredentialWritePlan {
    previous_reference: Option<String>,
    staged_reference: String,
}

impl CredentialWritePlan {
    pub fn stage(
        store: &dyn CredentialStore,
        model_id: &str,
        previous_reference: Option<&str>,
        secret: &str,
    ) -> CredentialResult<Self> {
        let secret = secret.trim();
        if secret.is_empty() {
            return Err(AppError::Validation(
                "设置 AI 模型凭据时 API Key 不能为空。".into(),
            ));
        }
        let staged_reference = format!(
            "{}:staged:{}",
            credential_key_for_ai_model(model_id),
            ids::timestamped_id("write")
        );
        store.set_secret(&staged_reference, secret)?;
        Ok(Self {
            previous_reference: previous_reference
                .map(str::trim)
                .filter(|value| !value.is_empty())
                .map(str::to_string),
            staged_reference,
        })
    }

    pub fn staged_reference(&self) -> &str {
        &self.staged_reference
    }

    pub fn previous_reference(&self) -> Option<&str> {
        self.previous_reference.as_deref()
    }

    pub fn rollback(self, store: &dyn CredentialStore) -> CredentialResult<()> {
        store.delete_secret(&self.staged_reference)
    }
}

pub fn credential_key_for_remote_api_token() -> &'static str {
    "settings:remote-api-token"
}

#[cfg(target_os = "macos")]
fn get_system_secret(key: &str) -> CredentialResult<Option<String>> {
    let output = Command::new(MACOS_SECURITY_PATH)
        .args(["find-generic-password", "-s", SERVICE_NAME, "-a", key, "-g"])
        .output()
        .map_err(|err| AppError::Infrastructure(format!("读取 macOS Keychain 失败: {err}")))?;

    if output.status.success() {
        return parse_keychain_password(&output.stderr).map(Some);
    }

    let stderr = String::from_utf8_lossy(&output.stderr);
    if stderr.contains("could not be found") || stderr.contains("-25300") {
        return Ok(None);
    }

    Err(AppError::Infrastructure(
        "读取 macOS Keychain 失败。".into(),
    ))
}

#[cfg(target_os = "macos")]
fn set_system_secret(key: &str, value: &str) -> CredentialResult<()> {
    let input = keychain_write_input(SERVICE_NAME, key, value)?;
    let mut child = Command::new(MACOS_SECURITY_PATH)
        .args(["-i"])
        .stdin(Stdio::piped())
        .stdout(Stdio::null())
        .stderr(Stdio::piped())
        .spawn()
        .map_err(|err| AppError::Infrastructure(format!("写入 macOS Keychain 失败: {err}")))?;

    let write_result = child
        .stdin
        .take()
        .ok_or_else(|| AppError::Infrastructure("无法打开 macOS Keychain 输入通道。".into()))
        .and_then(|mut stdin| {
            stdin.write_all(input.as_bytes()).map_err(|err| {
                AppError::Infrastructure(format!("写入 macOS Keychain 输入失败: {err}"))
            })
        });
    if let Err(error) = write_result {
        let _ = child.kill();
        let _ = child.wait();
        return Err(error);
    }

    let output = child
        .wait_with_output()
        .map_err(|err| AppError::Infrastructure(format!("等待 macOS Keychain 写入失败: {err}")))?;

    if output.status.success() {
        if get_system_secret(key)?.as_deref() != Some(value) {
            return Err(AppError::Infrastructure("系统凭据写入校验失败。".into()));
        }
        Ok(())
    } else {
        Err(AppError::Infrastructure(
            "写入 macOS Keychain 失败，请检查钥匙串权限。".into(),
        ))
    }
}

#[cfg(any(target_os = "macos", test))]
fn keychain_write_input(service: &str, key: &str, value: &str) -> CredentialResult<String> {
    if [service, key, value]
        .iter()
        .any(|part| part.contains(['\r', '\n', '\0']))
    {
        return Err(AppError::Validation("凭据不能包含换行或空字符。".into()));
    }
    let quote = |part: &str| format!("\"{}\"", part.replace('\\', "\\\\").replace('\"', "\\\""));
    // Interactive command input keeps the secret out of argv. Unlike -w with
    // no value, this does not silently store an empty password on macOS.
    Ok(format!(
        "add-generic-password -U -s {} -a {} -w {}\n",
        quote(service),
        quote(key),
        quote(value)
    ))
}

#[cfg(any(target_os = "macos", test))]
fn parse_keychain_password(output: &[u8]) -> CredentialResult<String> {
    let failed = || AppError::Infrastructure("系统凭据格式无效。".into());
    let text = std::str::from_utf8(output).map_err(|_| failed())?.trim();
    let value = text.strip_prefix("password: ").ok_or_else(failed)?;
    if let Some(encoded) = value.strip_prefix("0x") {
        let encoded = encoded.split_whitespace().next().ok_or_else(failed)?;
        if encoded.len() % 2 != 0 {
            return Err(failed());
        }
        let bytes = encoded
            .as_bytes()
            .chunks_exact(2)
            .map(|pair| {
                let digits = std::str::from_utf8(pair).map_err(|_| failed())?;
                u8::from_str_radix(digits, 16).map_err(|_| failed())
            })
            .collect::<CredentialResult<Vec<_>>>()?;
        return String::from_utf8(bytes).map_err(|_| failed());
    }
    value
        .strip_prefix('"')
        .and_then(|v| v.strip_suffix('"'))
        .map(str::to_string)
        .ok_or_else(failed)
}

#[cfg(target_os = "macos")]
fn delete_system_secret(key: &str) -> CredentialResult<()> {
    let output = Command::new(MACOS_SECURITY_PATH)
        .args(["delete-generic-password", "-s", SERVICE_NAME, "-a", key])
        .output()
        .map_err(|err| AppError::Infrastructure(format!("删除 macOS Keychain 凭据失败: {err}")))?;

    if output.status.success() {
        return Ok(());
    }

    let stderr = String::from_utf8_lossy(&output.stderr);
    if stderr.contains("could not be found") || stderr.contains("-25300") {
        return Ok(());
    }

    Err(AppError::Infrastructure(format!(
        "删除 macOS Keychain 凭据失败: {}",
        stderr.trim()
    )))
}

#[cfg(windows)]
mod windows_credential_store {
    use std::ptr;

    use windows_sys::Win32::Foundation::{GetLastError, ERROR_NOT_FOUND};
    use windows_sys::Win32::Security::Credentials::{
        CredDeleteW, CredFree, CredReadW, CredWriteW, CREDENTIALW, CRED_PERSIST_LOCAL_MACHINE,
        CRED_TYPE_GENERIC,
    };

    use super::{AppError, CredentialResult, SERVICE_NAME};

    pub fn get_system_secret(key: &str) -> CredentialResult<Option<String>> {
        let target = to_wide(&windows_target_name(key));
        let mut credential: *mut CREDENTIALW = ptr::null_mut();
        let success = unsafe {
            CredReadW(
                target.as_ptr(),
                CRED_TYPE_GENERIC,
                0,
                &mut credential as *mut _,
            )
        };

        if success == 0 {
            let error = unsafe { GetLastError() };
            if error == ERROR_NOT_FOUND {
                return Ok(None);
            }
            return Err(AppError::Infrastructure(format!(
                "读取 Windows Credential Manager 失败，错误码: {error}"
            )));
        }

        let secret = unsafe {
            let credential_ref = &*credential;
            let bytes = std::slice::from_raw_parts(
                credential_ref.CredentialBlob,
                credential_ref.CredentialBlobSize as usize,
            );
            let value = String::from_utf8(bytes.to_vec()).map_err(|err| {
                AppError::Infrastructure(format!("解析 Windows Credential Manager 凭据失败: {err}"))
            })?;
            CredFree(credential.cast());
            value
        };

        Ok(Some(secret))
    }

    pub fn set_system_secret(key: &str, value: &str) -> CredentialResult<()> {
        let mut target = to_wide(&windows_target_name(key));
        let mut username = to_wide("Liberty");
        let mut credential_blob = value.as_bytes().to_vec();
        let credential = CREDENTIALW {
            Type: CRED_TYPE_GENERIC,
            TargetName: target.as_mut_ptr(),
            CredentialBlobSize: credential_blob.len() as u32,
            CredentialBlob: credential_blob.as_mut_ptr(),
            Persist: CRED_PERSIST_LOCAL_MACHINE,
            UserName: username.as_mut_ptr(),
            ..Default::default()
        };

        let success = unsafe { CredWriteW(&credential, 0) };
        if success == 0 {
            let error = unsafe { GetLastError() };
            return Err(AppError::Infrastructure(format!(
                "写入 Windows Credential Manager 失败，错误码: {error}"
            )));
        }

        Ok(())
    }

    pub fn delete_system_secret(key: &str) -> CredentialResult<()> {
        let target = to_wide(&windows_target_name(key));
        let success = unsafe { CredDeleteW(target.as_ptr(), CRED_TYPE_GENERIC, 0) };
        if success == 0 {
            let error = unsafe { GetLastError() };
            if error != ERROR_NOT_FOUND {
                return Err(AppError::Infrastructure(format!(
                    "删除 Windows Credential Manager 凭据失败，错误码: {error}"
                )));
            }
        }
        Ok(())
    }

    fn windows_target_name(key: &str) -> String {
        format!("{SERVICE_NAME}:{key}")
    }

    fn to_wide(value: &str) -> Vec<u16> {
        value.encode_utf16().chain(std::iter::once(0)).collect()
    }
}

#[cfg(windows)]
use windows_credential_store::{delete_system_secret, get_system_secret, set_system_secret};

#[cfg(not(any(target_os = "macos", windows)))]
fn get_system_secret(_key: &str) -> CredentialResult<Option<String>> {
    Ok(None)
}

#[cfg(not(any(target_os = "macos", windows)))]
fn set_system_secret(_key: &str, _value: &str) -> CredentialResult<()> {
    Err(AppError::Infrastructure(
        "当前平台暂不支持系统凭据存储。".into(),
    ))
}

#[cfg(not(any(target_os = "macos", windows)))]
fn delete_system_secret(_key: &str) -> CredentialResult<()> {
    Ok(())
}

#[cfg(test)]
mod keychain_protocol_tests {
    use super::*;

    #[test]
    fn stdin_command_quotes_values_without_allowing_another_command() {
        let input =
            keychain_write_input("synthetic-service", "synthetic-account", "a\"b\\c $literal")
                .unwrap();
        assert_eq!(input, "add-generic-password -U -s \"synthetic-service\" -a \"synthetic-account\" -w \"a\\\"b\\\\c $literal\"\n");
        assert!(keychain_write_input("service", "account", "value\nquit").is_err());
        assert!(keychain_write_input("service", "account\nquit", "value").is_err());
    }

    #[test]
    fn reads_explicit_hex_without_misinterpreting_hex_like_ascii() {
        assert_eq!(
            parse_keychain_password(b"password: \"deadbeef\"\n").unwrap(),
            "deadbeef"
        );
        assert_eq!(
            parse_keychain_password(b"password: 0xe6b58be8af95  \"display-only\"\n").unwrap(),
            "测试"
        );
        assert!(parse_keychain_password(b"password: 0xzz").is_err());
        assert!(parse_keychain_password(b"invalid").is_err());
    }
}
