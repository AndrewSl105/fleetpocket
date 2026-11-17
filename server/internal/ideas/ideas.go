// Package ideas is the Ideas board (docs/SPEC-server.md "Ideas board"):
// cards captured from the web (text, files, links, voice notes transcribed
// with STT_PROVIDER) and Telegram, ordered in columns, turned into tasks,
// and moved automatically when those tasks start, merge or fail.
package ideas

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/andrewsl105/fleetpocket/server/internal/attachments"
	"github.com/andrewsl105/fleetpocket/server/internal/events"
	"github.com/andrewsl105/fleetpocket/server/internal/store"
)

// EventIdea is the backend event on a task whose idea moved because of it.
const EventIdea = "idea"

// ErrDisabled: the board is off (settings `ideas.enabled`).
var ErrDisabled = errors.New("the ideas board is off; turn it on in Settings")

// ErrInvalid is a validation error (400).
var ErrInvalid = errors.New("invalid")

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
}

// Transcriber turns a recording into text (the STT_PROVIDER pipeline the
// Telegram bot uses; telegram.OpenAITranscriber / WhisperCPPTranscriber).
type Transcriber interface {
	Transcribe(ctx context.Context, name, mime string, data []byte) (string, error)
}

type Service struct {
	Store *store.Store
	Files *attachments.Files
	Hub   *events.Hub
	// STT transcribes voice notes; nil (STT_PROVIDER=off) saves them without a transcript.
	STT Transcriber
	// HTTP fetches link previews; nil: SafeClient(). NoPreviews skips them.
	HTTP       *http.Client
	NoPreviews bool
	// Now is the clock; nil: time.Now.
	Now func() time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Settings returns the board settings.
func (s *Service) Settings(ctx context.Context) (store.IdeaSettings, error) {
	return s.Store.IdeaSettings(ctx)
}

// Enabled reports whether the board is on.
func (s *Service) Enabled(ctx context.Context) bool {
	c, err := s.Store.IdeaSettings(ctx)
	return err == nil && c.Enabled
}

func (s *Service) requireOn(ctx context.Context) (store.IdeaSettings, error) {
	c, err := s.Store.IdeaSettings(ctx)
	if err != nil {
		return c, err
	}
	if !c.Enabled {
		return c, ErrDisabled
	}
	return c, nil
}

// SetSettings validates and saves the board settings. Columns are the whole
// list in order: the five default ids must stay, new columns come without
// an id, at most MaxIdeaColumns, and an added column can go only when empty.
func (s *Service) SetSettings(ctx context.Context, in store.IdeaSettings) (store.IdeaSettings, error) {
	cur, err := s.Store.IdeaSettings(ctx)
	if err != nil {
		return cur, err
	}
	if in.Columns == nil {
		in.Columns = cur.Columns
	}
	if len(in.Columns) > store.MaxIdeaColumns {
		return cur, invalid("at most %d columns", store.MaxIdeaColumns)
	}
	seen := map[string]bool{}
	out := make([]store.IdeaColumn, 0, len(in.Columns))
	for _, c := range in.Columns {
		c.Name = strings.Join(strings.Fields(c.Name), " ")
		if c.Name == "" || utf8.RuneCountInString(c.Name) > 40 {
			return cur, invalid("column names must be 1 to 40 characters")
		}
		if c.ID == "" {
			c.ID = newColumnID()
		} else if !cur.HasColumn(c.ID) {
			return cur, invalid("unknown column %q", c.ID)
		}
		if seen[c.ID] {
			return cur, invalid("column %q is listed twice", c.ID)
		}
		seen[c.ID] = true
		out = append(out, c)
	}
	for _, d := range store.DefaultIdeaColumns() {
		if !seen[d.ID] {
			return cur, invalid("the %s column cannot be removed (rename it instead)", d.Name)
		}
	}
	for _, c := range cur.Columns {
		if seen[c.ID] {
			continue
		}
		n, err := s.Store.CountIdeasInColumn(ctx, c.ID)
		if err != nil {
			return cur, err
		}
		if n > 0 {
			return cur, fmt.Errorf("%w: the %s column still has %d card(s), archived ones included; move or delete them first", store.ErrConflict, c.Name, n)
		}
	}
	in.Columns = out
	if err := s.Store.SetIdeaSettings(ctx, in); err != nil {
		return cur, err
	}
	return in, nil
}

func newColumnID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return "c" + hex.EncodeToString(b)
}

// TitleFromText is the first sentence of text, cut to its first eight
// words ("…" when cut); "" for text without words.
func TitleFromText(text string) string {
	text = strings.TrimSpace(text)
	if i := strings.IndexFunc(text, func(r rune) bool { return r == '\n' }); i >= 0 {
		text = text[:i]
	}
	// End of the first sentence: . ! ? followed by a space or the end.
	for i, r := range text {
		if r == '.' || r == '!' || r == '?' || r == '…' {
			rest := text[i+utf8.RuneLen(r):]
			if rest == "" || unicode.IsSpace([]rune(rest)[0]) {
				text = text[:i]
				break
			}
		}
	}
	words := strings.Fields(text)
	if len(words) == 0 {
		return ""
	}
	title := strings.Join(words[:min(8, len(words))], " ")
	title = strings.TrimRight(title, ",;:-–—")
	if len(words) > 8 {
		title += "…"
	}
	return cut(title, 120)
}

func cut(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}

// VoiceTitle is the title of a voice note without a transcript.
func (s *Service) VoiceTitle() string {
	return "Voice note " + s.now().Format("Jan 2 15:04")
}

// Input is a new idea.
type Input struct {
	Title     string   `json:"title"`
	Body      string   `json:"body"`
	ProjectID *string  `json:"project_id"`
	Column    string   `json:"column"`
	Tags      []string `json:"tags"`
	// Links are saved as link attachments (previews fetched).
	Links  []string `json:"links"`
	Source string   `json:"-"`
}

// Patch changes an idea (nil fields stay; project_id "" clears it).
type Patch struct {
	Title     *string   `json:"title"`
	Body      *string   `json:"body"`
	ProjectID *string   `json:"project_id"`
	Tags      *[]string `json:"tags"`
}

func (s *Service) fields(cfg store.IdeaSettings, in Input) (store.IdeaFields, error) {
	f := store.IdeaFields{ProjectID: in.ProjectID, Title: strings.TrimSpace(in.Title), Body: strings.TrimSpace(in.Body),
		Column: in.Column, Tags: in.Tags, Source: in.Source}
	if f.Column == "" {
		f.Column = store.IdeaInbox
	}
	if !cfg.HasColumn(f.Column) {
		return f, invalid("unknown column %q", f.Column)
	}
	if f.Title == "" {
		f.Title = TitleFromText(f.Body)
	}
	if utf8.RuneCountInString(f.Title) > 200 {
		return f, invalid("the title is longer than 200 characters")
	}
	if len(f.Body) > 64<<10 {
		return f, invalid("the text is longer than 64 KB")
	}
	if len(f.Tags) > 20 {
		return f, invalid("at most 20 tags")
	}
	for _, t := range f.Tags {
		if utf8.RuneCountInString(t) > 40 {
			return f, invalid("tags are at most 40 characters")
		}
	}
	return f, nil
}

// fileRows turns validated uploads into attachment rows and their files.
func fileRows(imgs []*attachments.Image) ([]store.NewIdeaAttachment, []attachments.IdeaFile) {
	var rows []store.NewIdeaAttachment
	var files []attachments.IdeaFile
	for _, im := range imgs {
		rows = append(rows, store.NewIdeaAttachment{Kind: store.IdeaFile, Name: im.Name, Mime: im.Mime, Size: int64(len(im.Data)),
			Width: im.Width, Height: im.Height, HasThumb: im.Thumb != nil})
		files = append(files, attachments.IdeaFile{Data: im.Data, Thumb: im.Thumb})
	}
	return rows, files
}

// Create adds an idea with its files (validated uploads) and links.
func (s *Service) Create(ctx context.Context, in Input, imgs []*attachments.Image) (*store.Idea, error) {
	cfg, err := s.requireOn(ctx)
	if err != nil {
		return nil, err
	}
	return s.create(ctx, cfg, in, imgs, nil)
}

func (s *Service) create(ctx context.Context, cfg store.IdeaSettings, in Input, imgs []*attachments.Image, extra *extraRow) (*store.Idea, error) {
	f, err := s.fields(cfg, in)
	if err != nil {
		return nil, err
	}
	rows, files := fileRows(imgs)
	if len(imgs) > attachments.MaxFiles {
		return nil, invalid("at most %d files", attachments.MaxFiles)
	}
	links, err := s.linkRows(ctx, in.Links)
	if err != nil {
		return nil, err
	}
	for _, l := range links {
		rows, files = append(rows, l.row), append(files, l.file)
	}
	if extra != nil {
		rows, files = append(rows, extra.row), append(files, extra.file)
	}
	if f.Title == "" {
		switch {
		case extra != nil && extra.row.Kind == store.IdeaVoice:
			f.Title = s.VoiceTitle()
		case len(links) > 0 && links[0].row.LinkTitle != nil:
			f.Title = cut(*links[0].row.LinkTitle, 120)
		case len(rows) > 0:
			f.Title = cut(rows[0].Name, 120)
			if rows[0].Kind == store.IdeaLink && rows[0].URL != nil {
				f.Title = cut(*rows[0].URL, 120)
			}
		default:
			return nil, invalid("an idea needs a title, text, a file, a link or a voice note")
		}
	}
	var place func(int64, []*store.IdeaAttachment) error
	if s.Files != nil && len(rows) > 0 {
		release := s.Files.Hold()
		defer release()
		place = s.Files.PlaceIdea(files)
	}
	return s.Store.CreateIdea(ctx, f, rows, place)
}

type extraRow struct {
	row  store.NewIdeaAttachment
	file attachments.IdeaFile
}

// linkRows validates links and fetches their previews (title, og:image
// thumbnail). A preview that fails keeps the bare URL.
func (s *Service) linkRows(ctx context.Context, links []string) ([]extraRow, error) {
	if len(links) > attachments.MaxFiles {
		return nil, invalid("at most %d links", attachments.MaxFiles)
	}
	var out []extraRow
	for _, raw := range links {
		u, err := ParseLink(raw)
		if err != nil {
			return nil, invalid("%s", err.Error())
		}
		link := u.String()
		r := extraRow{row: store.NewIdeaAttachment{Kind: store.IdeaLink, Name: u.Host, URL: &link}}
		if !s.NoPreviews {
			c := s.HTTP
			if c == nil {
				c = SafeClient()
			}
			p, img, err := FetchPreview(ctx, c, link)
			if err != nil {
				slog.Info("ideas: link preview", "host", u.Host, "err", err)
			}
			if p.Title != "" {
				title := p.Title
				r.row.LinkTitle = &title
			}
			if img != nil {
				if im, err := attachments.Process("og-image", img); err == nil && im.Thumb != nil {
					r.file.Thumb, r.row.HasThumb, r.row.Width, r.row.Height = im.Thumb, true, im.Width, im.Height
				}
			}
		}
		out = append(out, r)
	}
	return out, nil
}

// Voice is a recording to save.
type Voice struct {
	Data       []byte
	Mime       string // declared type ("audio/webm;codecs=opus")
	Name       string
	DurationMS int
}

func (s *Service) voiceRow(ctx context.Context, v Voice) (extraRow, string, error) {
	mt, ok := attachments.AudioType(v.Mime)
	if !ok {
		return extraRow{}, "", invalid("a voice note must be WebM, Ogg, MP4/M4A, AAC, MP3 or WAV audio (got %q)", v.Mime)
	}
	if len(v.Data) == 0 {
		return extraRow{}, "", invalid("the recording is empty")
	}
	if len(v.Data) > attachments.MaxFileBytes {
		return extraRow{}, "", invalid("the recording is larger than %d MB", attachments.MaxFileBytes>>20)
	}
	dur := min(max(v.DurationMS, 0), int(time.Hour/time.Millisecond))
	name := strings.TrimSpace(v.Name)
	if name == "" {
		name = "voice-note"
	}
	row := store.NewIdeaAttachment{Kind: store.IdeaVoice, Name: cut(name, 100), Mime: mt, Size: int64(len(v.Data)), DurationMS: &dur}
	transcript := ""
	if s.STT != nil {
		tctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		text, err := s.STT.Transcribe(tctx, name, v.Mime, v.Data)
		cancel()
		if err != nil {
			slog.Warn("ideas: transcribe", "err", err)
		} else if text = strings.TrimSpace(text); text != "" {
			transcript = text
			row.Transcript = &transcript
		}
	}
	return extraRow{row: row, file: attachments.IdeaFile{Data: v.Data}}, transcript, nil
}

// CreateVoice saves a voice note as a new idea: the transcript becomes its
// text and the title its first words ("Voice note <time>" without STT).
func (s *Service) CreateVoice(ctx context.Context, in Input, v Voice) (*store.Idea, error) {
	cfg, err := s.requireOn(ctx)
	if err != nil {
		return nil, err
	}
	return s.CreateVoiceWith(ctx, cfg, in, v, nil)
}

// CreateVoiceWith is CreateVoice with the settings already read and extra
// files (Telegram: a voice message with photos is rare, but an album caption is not).
func (s *Service) CreateVoiceWith(ctx context.Context, cfg store.IdeaSettings, in Input, v Voice, imgs []*attachments.Image) (*store.Idea, error) {
	r, transcript, err := s.voiceRow(ctx, v)
	if err != nil {
		return nil, err
	}
	if transcript != "" {
		if strings.TrimSpace(in.Body) != "" {
			in.Body = strings.TrimSpace(in.Body) + "\n\n" + transcript
		} else {
			in.Body = transcript
		}
	}
	return s.create(ctx, cfg, in, imgs, &r)
}

// CreateWith is Create with the settings already read (Telegram).
func (s *Service) CreateWith(ctx context.Context, cfg store.IdeaSettings, in Input, imgs []*attachments.Image) (*store.Idea, error) {
	return s.create(ctx, cfg, in, imgs, nil)
}

// AddVoice adds a voice note to an idea; its transcript is appended to the text.
func (s *Service) AddVoice(ctx context.Context, id int64, v Voice) (*store.Idea, error) {
	idea, err := s.get(ctx, id)
	if err != nil {
		return nil, err
	}
	r, transcript, err := s.voiceRow(ctx, v)
	if err != nil {
		return nil, err
	}
	if err := s.addRows(ctx, id, []extraRow{r}); err != nil {
		return nil, err
	}
	if transcript == "" {
		return s.Store.GetIdea(ctx, id)
	}
	body := transcript
	if strings.TrimSpace(idea.Body) != "" {
		body = strings.TrimRight(idea.Body, "\n") + "\n\n" + transcript
	}
	return s.Store.UpdateIdea(ctx, id, store.IdeaPatch{Body: &body})
}

func (s *Service) addRows(ctx context.Context, id int64, extra []extraRow) error {
	rows := make([]store.NewIdeaAttachment, 0, len(extra))
	files := make([]attachments.IdeaFile, 0, len(extra))
	for _, e := range extra {
		rows, files = append(rows, e.row), append(files, e.file)
	}
	var place func(int64, []*store.IdeaAttachment) error
	if s.Files != nil {
		release := s.Files.Hold()
		defer release()
		place = s.Files.PlaceIdea(files)
	}
	_, err := s.Store.AddIdeaAttachments(ctx, id, rows, place)
	return err
}

// AddFiles adds validated uploads to an idea.
func (s *Service) AddFiles(ctx context.Context, id int64, imgs []*attachments.Image) (*store.Idea, error) {
	idea, err := s.get(ctx, id)
	if err != nil {
		return nil, err
	}
	if len(idea.Attachments)+len(idea.VoiceNotes)+len(imgs) > 50 {
		return nil, invalid("an idea holds at most 50 attachments")
	}
	rows, files := fileRows(imgs)
	extra := make([]extraRow, len(rows))
	for i := range rows {
		extra[i] = extraRow{row: rows[i], file: files[i]}
	}
	if err := s.addRows(ctx, id, extra); err != nil {
		return nil, err
	}
	return s.Store.GetIdea(ctx, id)
}

// AddLink saves a link on an idea (with its preview when it can be fetched).
func (s *Service) AddLink(ctx context.Context, id int64, link string) (*store.Idea, error) {
	if _, err := s.get(ctx, id); err != nil {
		return nil, err
	}
	rows, err := s.linkRows(ctx, []string{link})
	if err != nil {
		return nil, err
	}
	if err := s.addRows(ctx, id, rows); err != nil {
		return nil, err
	}
	return s.Store.GetIdea(ctx, id)
}

// RemoveAttachment deletes an attachment and its files.
func (s *Service) RemoveAttachment(ctx context.Context, id, attachmentID int64) (*store.Idea, error) {
	if _, err := s.requireOn(ctx); err != nil {
		return nil, err
	}
	a, err := s.Store.GetIdeaAttachment(ctx, id, attachmentID)
	if err != nil {
		return nil, err
	}
	if err := s.Store.DeleteIdeaAttachment(ctx, id, attachmentID); err != nil {
		return nil, err
	}
	if s.Files != nil {
		s.Files.RemoveIdeaAttachment(a)
	}
	return s.Store.GetIdea(ctx, id)
}

func (s *Service) get(ctx context.Context, id int64) (*store.Idea, error) {
	if _, err := s.requireOn(ctx); err != nil {
		return nil, err
	}
	return s.Store.GetIdea(ctx, id)
}

// Get returns one idea.
func (s *Service) Get(ctx context.Context, id int64) (*store.Idea, error) { return s.get(ctx, id) }

// List returns the board or the archive.
func (s *Service) List(ctx context.Context, f store.IdeaFilter) ([]*store.Idea, error) {
	if _, err := s.requireOn(ctx); err != nil {
		return nil, err
	}
	return s.Store.ListIdeas(ctx, f)
}

// Update changes an idea's title, text, project or tags.
func (s *Service) Update(ctx context.Context, id int64, p Patch) (*store.Idea, error) {
	cfg, err := s.requireOn(ctx)
	if err != nil {
		return nil, err
	}
	cur, err := s.Store.GetIdea(ctx, id)
	if err != nil {
		return nil, err
	}
	in := Input{Title: cur.Title, Body: cur.Body, Column: cur.Column, Tags: cur.Tags}
	if p.Title != nil {
		in.Title = *p.Title
	}
	if p.Body != nil {
		in.Body = *p.Body
	}
	if p.Tags != nil {
		in.Tags = *p.Tags
	}
	f, err := s.fields(cfg, in)
	if err != nil {
		return nil, err
	}
	if f.Title == "" {
		return nil, invalid("the title cannot be empty")
	}
	patch := store.IdeaPatch{ProjectID: p.ProjectID}
	if p.Title != nil {
		patch.Title = &f.Title
	}
	if p.Body != nil {
		patch.Body = &f.Body
	}
	if p.Tags != nil {
		patch.Tags = &f.Tags
	}
	return s.Store.UpdateIdea(ctx, id, patch)
}

// Move puts an idea into column after the card afterID (nil: on top).
func (s *Service) Move(ctx context.Context, id int64, column string, afterID *int64) (*store.Idea, error) {
	cfg, err := s.requireOn(ctx)
	if err != nil {
		return nil, err
	}
	if !cfg.HasColumn(column) {
		return nil, invalid("unknown column %q", column)
	}
	return s.Store.MoveIdea(ctx, id, column, afterID)
}

// SetArchived archives an idea or brings it back.
func (s *Service) SetArchived(ctx context.Context, id int64, archived bool) (*store.Idea, error) {
	if _, err := s.requireOn(ctx); err != nil {
		return nil, err
	}
	return s.Store.SetIdeaArchived(ctx, id, archived)
}

// Delete removes an archived idea for good, with its files.
func (s *Service) Delete(ctx context.Context, id int64) error {
	if _, err := s.requireOn(ctx); err != nil {
		return err
	}
	if err := s.Store.DeleteIdea(ctx, id); err != nil {
		return err
	}
	if s.Files != nil {
		if err := s.Files.RemoveIdea(id); err != nil {
			slog.Warn("ideas: remove files", "idea_id", id, "err", err)
		}
	}
	return nil
}

// CheckLink is called before a task is created from an idea, so a bad
// idea_id fails before the task exists.
func (s *Service) CheckLink(ctx context.Context, ideaID int64) error {
	idea, err := s.get(ctx, ideaID)
	if errors.Is(err, store.ErrNotFound) {
		return invalid("idea %d does not exist", ideaID)
	}
	if err != nil {
		return err
	}
	if idea.ArchivedAt != nil {
		return fmt.Errorf("%w: idea %d is archived", store.ErrConflict, ideaID)
	}
	return nil
}

// LinkTask records that task was started from the idea and moves the card
// to In progress (an automatic move: a timeline event on the task, which
// the Telegram bot announces, and a note on the card).
func (s *Service) LinkTask(ctx context.Context, ideaID int64, task *store.Task) error {
	if err := s.Store.LinkIdeaTask(ctx, ideaID, task.ID); err != nil {
		return err
	}
	s.autoMove(ctx, ideaID, task, store.IdeaInProgress, "started", fmt.Sprintf("task #%d started from it", task.ID))
	return nil
}

// autoMove moves an idea for a task and records it on the task.
func (s *Service) autoMove(ctx context.Context, ideaID int64, task *store.Task, column, action, reason string) {
	cfg, err := s.Store.IdeaSettings(ctx)
	if err != nil {
		return
	}
	name := cfg.ColumnName(column)
	idea, moved, err := s.Store.MoveIdeaAuto(ctx, ideaID, column, fmt.Sprintf("Moved to %s: %s", name, reason))
	if err != nil {
		slog.Warn("ideas: auto move", "idea_id", ideaID, "task_id", task.ID, "err", err)
		return
	}
	s.record(ctx, task.ID, map[string]any{"idea_id": idea.ID, "title": idea.Title, "action": action, "column": column,
		"column_name": name, "moved": moved, "reason": reason})
}

func (s *Service) record(ctx context.Context, taskID int64, payload map[string]any) {
	ev, err := s.Store.AddBackendEvent(ctx, taskID, nil, EventIdea, payload)
	if err != nil {
		slog.Warn("ideas: event", "task_id", taskID, "err", err)
		return
	}
	if s.Hub != nil {
		s.Hub.PublishEvent(ev)
	}
}

// Outcome is what a task's status change means for its ideas.
type Outcome int

const (
	OutcomeNone Outcome = iota
	OutcomeDone
	OutcomeFailed
)

// TaskOutcome classifies a task status change: a merged PR, or a local
// branch merged into the owner's branch, is done; a failure is failed. A PR
// closed without merging, a discarded branch, a cancel or "committed
// locally" are none (the owner decides where the card goes).
func TaskOutcome(status, reason string) Outcome {
	switch status {
	case "completed":
		if reason == "PR merged" || strings.HasPrefix(reason, "branch merged into ") {
			return OutcomeDone
		}
	case "failed":
		return OutcomeFailed
	}
	return OutcomeNone
}

// Watch moves the ideas of tasks that finish (done on a merge, ✕ on a
// failure, which the card derives from the task). Changes that happen
// while the server is down are not replayed. It returns when ctx ends.
func (s *Service) Watch(ctx context.Context) {
	if s.Hub == nil {
		return
	}
	ch, cancel := s.Hub.Subscribe(256)
	defer cancel()
	for {
		select {
		case <-ctx.Done():
			return
		case m, ok := <-ch:
			if !ok {
				return
			}
			if m.Task == nil || len(m.Task.StatusEvents) == 0 {
				continue
			}
			s.OnTaskStatus(ctx, m.Task)
		}
	}
}

// OnTaskStatus applies a task's new status to its ideas.
func (s *Service) OnTaskStatus(ctx context.Context, t *store.Task) {
	reason := ""
	if t.StatusReason != nil {
		reason = *t.StatusReason
	}
	out := TaskOutcome(t.Status, reason)
	if out == OutcomeNone {
		return
	}
	ids, err := s.Store.IdeaIDsForTask(ctx, t.ID)
	if err != nil || len(ids) == 0 {
		return
	}
	if !s.Enabled(ctx) {
		return
	}
	for _, id := range ids {
		switch out {
		case OutcomeDone:
			what := "PR merged"
			if strings.HasPrefix(reason, "branch merged") {
				what = reason
			}
			s.autoMove(ctx, id, t, store.IdeaDone, "done", fmt.Sprintf("task #%d, %s", t.ID, what))
		case OutcomeFailed:
			idea, err := s.Store.GetIdea(ctx, id)
			if err != nil {
				continue
			}
			s.record(ctx, t.ID, map[string]any{"idea_id": idea.ID, "title": idea.Title, "action": "failed", "column": idea.Column,
				"column_name": "", "moved": false, "reason": fmt.Sprintf("task #%d failed", t.ID)})
		}
	}
}
