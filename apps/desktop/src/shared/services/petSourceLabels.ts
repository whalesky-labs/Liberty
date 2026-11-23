import type { LocaleCode } from "@/shared/types/meeting";
import { petSourceLabels, type LocalizedPetLabel } from "@liberty-resources/i18n/petSourceLabels";

export { petSourceLabels, type LocalizedPetLabel };

export function localizedPetLabel(label: LocalizedPetLabel, locale: LocaleCode) {
  return locale === "en-US" ? label.en : label.zh;
}

export function petSourceLabel(source: string, locale: LocaleCode) {
  const label = petSourceLabels[source];
  return label ? localizedPetLabel(label, locale) : null;
}

export function petSourceLabelOrUnknown(source: string, locale: LocaleCode) {
  return petSourceLabel(source, locale) ?? (locale === "en-US" ? "Unknown" : "未知");
}
