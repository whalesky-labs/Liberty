//! Cross-executable ownership of a Liberty profile. The Go client uses the same
//! persistent lock file and OS lock primitive. Never unlink a lock file: doing so
//! could let another process lock a different inode while this owner is alive.
use std::{fs::OpenOptions, path::Path};

pub struct ProfileLock {
    _file: std::fs::File,
}

impl ProfileLock {
    pub fn acquire(directory: &Path) -> Result<Self, &'static str> {
        let directory = directory
            .canonicalize()
            .map_err(|_| "profile_lock_failed")?;
        let path = directory.join(".liberty-profile.lock");
        let mut options = OpenOptions::new();
        options.read(true).write(true).create(true).truncate(false);
        #[cfg(unix)]
        {
            use std::os::unix::fs::OpenOptionsExt;
            options.mode(0o600).custom_flags(libc::O_NOFOLLOW);
        }
        #[cfg(windows)]
        {
            use std::os::windows::fs::OpenOptionsExt;
            options.custom_flags(0x00200000); // FILE_FLAG_OPEN_REPARSE_POINT
        }
        let file = options.open(path).map_err(|_| "profile_lock_failed")?;
        let metadata = file.metadata().map_err(|_| "profile_lock_failed")?;
        if !metadata.is_file() || metadata.file_type().is_symlink() {
            return Err("profile_lock_failed");
        }
        #[cfg(unix)]
        {
            use std::os::unix::fs::MetadataExt;
            if metadata.nlink() != 1 {
                return Err("profile_lock_failed");
            }
        }
        #[cfg(windows)]
        {
            use std::os::windows::fs::MetadataExt;
            if metadata.file_attributes() & 0x00000400 != 0 {
                return Err("profile_lock_failed");
            }
        }
        file.try_lock().map_err(|error| match error {
            std::fs::TryLockError::WouldBlock => "profile_in_use",
            std::fs::TryLockError::Error(_) => "profile_lock_failed",
        })?;
        Ok(Self { _file: file })
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    #[ignore = "cross-language ownership probe via pnpm native:compat"]
    fn native_profile_probe() {
        let directory = std::env::var_os("LIBERTY_NATIVE_LOCK_PROBE_DIR")
            .expect("explicit synthetic profile directory");
        let expected = std::env::var("LIBERTY_NATIVE_LOCK_PROBE_EXPECT")
            .expect("explicit expected ownership state");
        let result = ProfileLock::acquire(Path::new(&directory));
        match expected.as_str() {
            "locked" => assert!(matches!(result, Err("profile_in_use"))),
            "available" => assert!(result.is_ok()),
            _ => panic!("invalid probe expectation"),
        }
    }

    #[test]
    fn owner_excludes_another_open_and_releases_on_drop() {
        let root =
            std::env::temp_dir().join(crate::infrastructure::ids::timestamped_id("profile-test"));
        std::fs::create_dir(&root).unwrap();
        let first = ProfileLock::acquire(&root).unwrap();
        assert!(matches!(ProfileLock::acquire(&root), Err("profile_in_use")));
        drop(first);
        drop(ProfileLock::acquire(&root).unwrap());
        assert!(root.join(".liberty-profile.lock").is_file());
        std::fs::remove_dir_all(root).unwrap();
    }

    #[cfg(unix)]
    #[test]
    fn rejects_symlinked_lock_without_touching_target() {
        let root = std::env::temp_dir().join(crate::infrastructure::ids::timestamped_id(
            "profile-symlink-test",
        ));
        std::fs::create_dir(&root).unwrap();
        let target = root.join("synthetic-target");
        std::fs::write(&target, "unchanged").unwrap();
        std::os::unix::fs::symlink(&target, root.join(".liberty-profile.lock")).unwrap();
        assert!(matches!(
            ProfileLock::acquire(&root),
            Err("profile_lock_failed")
        ));
        assert_eq!(std::fs::read_to_string(target).unwrap(), "unchanged");
        std::fs::remove_dir_all(root).unwrap();
    }
}
