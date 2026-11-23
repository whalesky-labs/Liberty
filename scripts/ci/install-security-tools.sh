#!/usr/bin/env bash
set -euo pipefail
mkdir -p "${RUNNER_TEMP}/liberty-security-tools" "${HOME}/.local/bin"
curl --location --fail --silent --show-error \
  --output "${RUNNER_TEMP}/liberty-security-tools/cargo-deny.tar.gz" \
  "https://github.com/EmbarkStudios/cargo-deny/releases/download/0.20.2/cargo-deny-0.20.2-x86_64-unknown-linux-musl.tar.gz"
printf '%s  %s\n' \
  '9f12ed4c49936e09b48bf862b595cde2fe64fcbd9d74dfacac6131ca824c8d5f' \
  "${RUNNER_TEMP}/liberty-security-tools/cargo-deny.tar.gz" | sha256sum --check
tar --extract --gzip --file "${RUNNER_TEMP}/liberty-security-tools/cargo-deny.tar.gz" --directory "${RUNNER_TEMP}/liberty-security-tools"
install -m 0755 "${RUNNER_TEMP}/liberty-security-tools/cargo-deny-0.20.2-x86_64-unknown-linux-musl/cargo-deny" "${HOME}/.local/bin/cargo-deny"
curl --location --fail --silent --show-error \
  --output "${RUNNER_TEMP}/liberty-security-tools/osv-scanner" \
  "https://github.com/google/osv-scanner/releases/download/v2.5.0/osv-scanner_linux_amd64"
printf '%s  %s\n' \
  'edcfc41d257db36148f065055655fe3fcfc434b0b423ea67468a84c207524e0c' \
  "${RUNNER_TEMP}/liberty-security-tools/osv-scanner" | sha256sum --check
install -m 0755 "${RUNNER_TEMP}/liberty-security-tools/osv-scanner" "${HOME}/.local/bin/osv-scanner"
echo "${HOME}/.local/bin" >> "${GITHUB_PATH}"
