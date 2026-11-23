package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

type MinutesMetadataProjection struct {
	Info     MinutesInfo `json:"info"`
	Source   string      `json:"source"`
	Warnings []string    `json:"warnings"`
}

func missingMinutesValue(value string) bool {
	switch strings.TrimSpace(value) {
	case "", "待补充", "待补充部门", "待补充姓名":
		return true
	}
	return false
}
func cleanMinutesInfo(info MinutesInfo) MinutesInfo {
	for _, field := range []*string{&info.MeetingName, &info.MeetingTime, &info.MeetingLocation, &info.Recorder, &info.Attendees, &info.Absentees, &info.Host, &info.Reviewer} {
		*field = strings.TrimSpace(*field)
		if missingMinutesValue(*field) {
			*field = ""
		}
	}
	return info
}

// ProjectMinutesMetadata preserves explicit user edits, including intentionally
// empty fields. Legacy fixed metadata is excluded using the original digest.
func ProjectMinutesMetadata(persisted *MinutesPayload, ai MinutesInfo) MinutesMetadataProjection {
	p := MinutesMetadataProjection{Source: "empty", Warnings: []string{}}
	var legacy MinutesInfo
	if persisted != nil {
		if persisted.SchemaVersion == 2 && persisted.MeetingInfoSource == "user" {
			p.Info, p.Source = cleanMinutesInfo(persisted.MeetingInfo), "user"
			return p
		}
		if persisted.SchemaVersion == 1 {
			legacy = persisted.MeetingInfo
			value := strings.TrimSpace(legacy.MeetingTime) + "\x00" + strings.TrimSpace(legacy.MeetingLocation) + "\x00" + strings.TrimSpace(legacy.Host)
			digest := sha256.Sum256([]byte(value))
			if hex.EncodeToString(digest[:]) == "87108e7392f309f74b2ddf92210cd982639c82bfe553467b54642c1dbb24a073" {
				p.Warnings = append(p.Warnings, "legacy_fixed_meeting_metadata_ignored")
				legacy.MeetingTime, legacy.MeetingLocation, legacy.Host = "", "", ""
			}
			legacy = cleanMinutesInfo(legacy)
		}
	}
	ai = cleanMinutesInfo(ai)
	if ai != (MinutesInfo{}) {
		p.Info, p.Source = ai, "ai"
	} else if legacy != (MinutesInfo{}) {
		p.Info, p.Source = legacy, "ai"
	}
	return p
}

func NormalizeMemberName(value string) string {
	value = strings.TrimRight(strings.TrimLeft(strings.TrimSpace(value), "【"), "】")
	for strings.HasSuffix(value, "（未发言）") {
		value = strings.TrimSuffix(value, "（未发言）")
	}
	for strings.HasSuffix(value, "(未发言)") {
		value = strings.TrimSuffix(value, "(未发言)")
	}
	return strings.TrimSpace(value)
}
