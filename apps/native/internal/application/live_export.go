package application

import (
	"context"
	"fmt"
	"html"
	"strings"

	"liberty.local/liberty/internal/domain"
)

func subtitleTime(ms int64, vtt bool) string {
	separator := ","
	if vtt {
		separator = "."
	}
	return fmt.Sprintf("%02d:%02d:%02d%s%03d", ms/3600000, (ms/60000)%60, (ms/1000)%60, separator, ms%1000)
}
func subtitleText(value string) string {
	return html.EscapeString(strings.Join(strings.Fields(value), " "))
}

// Export re-reads persisted finals after the save dialog grants a destination.
// A stopped session is required; retries and deletion cannot race this snapshot.
func (s *LiveService) Export(ctx context.Context, id, format string, publish func([]byte) error) error {
	if format != "srt" && format != "vtt" && format != "txt" {
		return domain.ErrLiveInput
	}
	if publish == nil {
		return domain.ErrLiveInput
	}
	ctx, done, err := s.operation(ctx)
	if err != nil {
		return err
	}
	defer done()
	// Freeze the history under the service lock; file I/O must not hold up
	// recognition events or capture from a different active session.
	data, err := func() ([]byte, error) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.closed {
			return nil, domain.ErrLiveClosed
		}
		if s.retrying || (s.active != nil && s.active.session.ID == id) {
			return nil, domain.ErrLiveBusy
		}
		session, err := s.repository.LiveSession(ctx, id)
		if err != nil {
			return nil, err
		}
		if domain.LiveActive(session.State) {
			return nil, domain.ErrLiveBusy
		}
		var output strings.Builder
		if format == "vtt" {
			output.WriteString("WEBVTT\n\n")
		}
		var after, index int64
		for {
			segments, err := s.repository.LiveSegments(ctx, id, after, 100)
			if err != nil {
				return nil, err
			}
			if len(segments) == 0 {
				break
			}
			for _, v := range segments {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				index++
				after = v.ID
				if format == "srt" {
					fmt.Fprintf(&output, "%d\n", index)
				}
				if format != "txt" {
					fmt.Fprintf(&output, "%s --> %s\n", subtitleTime(v.StartMS, format == "vtt"), subtitleTime(max(v.EndMS, v.StartMS+1), format == "vtt"))
					output.WriteString(subtitleText(v.Text))
				} else {
					fmt.Fprintf(&output, "[%s] %s", subtitleTime(v.StartMS, true), v.Text)
				}
				output.WriteByte('\n')
				if v.TranslationState == "completed" {
					if format == "txt" {
						output.WriteString(v.Translation)
					} else {
						output.WriteString(subtitleText(v.Translation))
					}
					output.WriteByte('\n')
				}
				output.WriteByte('\n')
				if output.Len() > 32*1024*1024 {
					return nil, domain.ErrLiveInput
				}
			}
		}
		return []byte(output.String()), nil
	}()
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return publish(data)
}
