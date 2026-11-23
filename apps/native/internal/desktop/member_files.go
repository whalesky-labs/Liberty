package desktop

import (
	"context"

	"github.com/egoist/mygo"
	"liberty.local/liberty/internal/domain"
	"liberty.local/liberty/internal/infrastructure/memberxlsx"
)

// NativeMemberFiles is retained only by the main-window resource controller.
// No path is accepted from a view; every read/write starts with a native dialog.
type NativeMemberFiles struct {
	Parent  *mygo.Window
	Service memberFileService
	dialogs memberFileDialogs
}

type memberFileService interface {
	Members(context.Context) ([]domain.MeetingMember, error)
	ImportMembers(context.Context, []domain.MeetingMember) (domain.MemberImportResult, error)
}

type memberFileDialogs interface {
	Open(mygo.OpenDialogOptions) ([]string, error)
	Save(mygo.SaveDialogOptions) (string, error)
}

func (f *NativeMemberFiles) fileDialogs() memberFileDialogs {
	if f.dialogs != nil {
		return f.dialogs
	}
	return mygo.Dialog
}

func (f *NativeMemberFiles) Import(ctx context.Context) (domain.MemberImportResult, bool, error) {
	if err := ctx.Err(); err != nil {
		return domain.MemberImportResult{}, false, err
	}
	paths, err := f.fileDialogs().Open(mygo.OpenDialogOptions{Parent: f.Parent, Filters: []mygo.FileFilter{{Name: "Excel", Extensions: []string{"xlsx"}}}})
	if ctx.Err() != nil {
		return domain.MemberImportResult{}, false, ctx.Err()
	}
	if err != nil {
		return domain.MemberImportResult{}, false, memberxlsx.ErrFile
	}
	if len(paths) == 0 {
		return domain.MemberImportResult{}, false, nil
	}
	if len(paths) != 1 {
		return domain.MemberImportResult{}, false, memberxlsx.ErrFile
	}
	values, err := memberxlsx.ReadFile(paths[0])
	if err != nil {
		return domain.MemberImportResult{}, true, err
	}
	if err := ctx.Err(); err != nil {
		return domain.MemberImportResult{}, false, err
	}
	result, err := f.Service.ImportMembers(ctx, values)
	return result, true, err
}
func (f *NativeMemberFiles) Export(ctx context.Context) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	filename, err := f.fileDialogs().Save(mygo.SaveDialogOptions{Parent: f.Parent, DefaultPath: "人员信息.xlsx", Filters: []mygo.FileFilter{{Name: "Excel", Extensions: []string{"xlsx"}}}})
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	if err != nil {
		return false, memberxlsx.ErrFile
	}
	if filename == "" {
		return false, nil
	}
	values, err := f.Service.Members(ctx)
	if err != nil {
		return true, err
	}
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	return true, memberxlsx.SaveFile(ctx, filename, values)
}
