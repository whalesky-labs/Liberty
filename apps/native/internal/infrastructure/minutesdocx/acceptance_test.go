//go:build document_acceptance

package minutesdocx

import (
	"os"
	"path/filepath"
	"testing"

	"liberty.local/liberty/internal/domain"
)

// Explicit synthetic artifact for the document renderer. Never reads user data.
func TestSyntheticWordAcceptance(t *testing.T) {
	dir := os.Getenv("LIBERTY_NATIVE_WORD_ACCEPTANCE_DIR")
	if !filepath.IsAbs(dir) {
		t.Fatal("explicit absolute acceptance directory required")
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		t.Fatal("acceptance directory unavailable")
	}
	document := domain.MinutesDocument{Title: "合成项目周会会议纪要", Info: domain.MinutesInfo{MeetingName: "合成项目周会", MeetingTime: "2026-11-17 10:00", MeetingLocation: "合成测试会议室", Recorder: "合成记录人", Attendees: "合成人员A、合成人员B", Host: "合成主持人", Reviewer: "合成审阅人"}, Topics: "资料库升级；会议纪要导出；下周验证安排", ClosingSummary: []string{"所有内容均为程序生成的合成验收资料。", "本次讨论确认了验证顺序和复核责任。"}, FallbackOverview: []string{"决策：", "继续进行跨平台验证。", "行动项：", "任务：复核导出排版；负责人：合成人员A；截止日期：下周"}, SpeechBlocks: []domain.MinutesSpeechBlock{
		{Name: "合成人员A", Department: "合成研发部", WeeklySummary: []string{"完成资料库升级与异常恢复测试。", "确认导出保留原有表格及段落样式。"}, NextWeekPlan: []string{"验证长会议内容的分页。", "补充旧任务导入后的重启回归。"}, Summary: []string{"保留原有业务与数据。"}},
		{Name: "合成人员B", Department: "合成验证部", WeeklySummary: []string{"核对中英文结果与历史总结。", "复核特殊字符 A&B、<测试> 和多段内容。"}, NextWeekPlan: []string{"执行输入法和独立窗口测试。"}, Summary: []string{"完成后逐项复核验收记录。"}},
	}}
	data, err := Encode(document)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(dir, "native-synthetic-minutes.docx"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal("acceptance output already exists or cannot be created")
	}
	_, err = f.Write(data)
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		t.Fatal("acceptance output write failed")
	}
}
