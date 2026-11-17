package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// SettingIdeas is the settings row of IdeaSettings (missing: board off,
// default columns).
const SettingIdeas = "ideas"

// The default columns. Their ids are fixed: auto-moves target in_progress
// and done by id, whatever the owner renamed them to, so they cannot be
// removed (only renamed and reordered).
const (
	IdeaInbox      = "inbox"
	IdeaShaping    = "shaping"
	IdeaReady      = "ready"
	IdeaInProgress = "in_progress"
	IdeaDone       = "done"
	// MaxIdeaColumns is how many columns a board may have.
	MaxIdeaColumns = 8
)

// IdeaColumn is one column of the board.
type IdeaColumn struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// DefaultIdeaColumns are the columns of a new board.
func DefaultIdeaColumns() []IdeaColumn {
	return []IdeaColumn{
		{IdeaInbox, "Inbox"}, {IdeaShaping, "Shaping"}, {IdeaReady, "Ready"}, {IdeaInProgress, "In progress"}, {IdeaDone, "Done"},
	}
}

// IsDefaultIdeaColumn reports whether id is one of the five fixed columns.
func IsDefaultIdeaColumn(id string) bool {
	for _, c := range DefaultIdeaColumns() {
		if c.ID == id {
			return true
		}
	}
	return false
}

// IdeaSettings are the board's settings.
type IdeaSettings struct {
	// Enabled shows the board (default false: today's behaviour).
	Enabled bool         `json:"enabled"`
	Columns []IdeaColumn `json:"columns"`
}

// HasColumn reports whether the board has a column with this id.
func (c IdeaSettings) HasColumn(id string) bool {
	for _, col := range c.Columns {
		if col.ID == id {
			return true
		}
	}
	return false
}

// ColumnName is the column's display name (its id when unknown).
func (c IdeaSettings) ColumnName(id string) string {
	for _, col := range c.Columns {
		if col.ID == id {
			return col.Name
		}
	}
	return id
}

// IdeaSettings returns the board settings; a missing row is a board that is
// off with the default columns.
func (s *Store) IdeaSettings(ctx context.Context) (IdeaSettings, error) {
	var c IdeaSettings
	var v string
	err := s.Pool.QueryRow(ctx, `SELECT value FROM settings WHERE key = $1`, SettingIdeas).Scan(&v)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return c, err
	}
	if err == nil {
		if err := json.Unmarshal([]byte(v), &c); err != nil {
			return c, err
		}
	}
	if len(c.Columns) == 0 {
		c.Columns = DefaultIdeaColumns()
	}
	return c, nil
}

// SetIdeaSettings replaces the board settings (validated by the caller).
func (s *Store) SetIdeaSettings(ctx context.Context, c IdeaSettings) error {
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	_, err = s.Pool.Exec(ctx, `INSERT INTO settings (key, value) VALUES ($1, $2)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`, SettingIdeas, string(b))
	return err
}

// Idea kinds of attachment.
const (
	IdeaFile  = "file"
	IdeaVoice = "voice"
	IdeaLink  = "link"
)

// IdeaAttachment is a file, voice note or link of an idea.
type IdeaAttachment struct {
	ID         int64     `json:"id"`
	IdeaID     int64     `json:"-"`
	Kind       string    `json:"kind"`
	Name       string    `json:"name"`
	Mime       string    `json:"mime"`
	Size       int64     `json:"size"`
	Width      int       `json:"width"`
	Height     int       `json:"height"`
	DurationMS *int      `json:"duration_ms"`
	Transcript *string   `json:"transcript"`
	URL        *string   `json:"url"`
	LinkTitle  *string   `json:"title"`
	HasThumb   bool      `json:"has_thumb"`
	CreatedAt  time.Time `json:"created_at"`
}

// NewIdeaAttachment is an attachment row to insert.
type NewIdeaAttachment struct {
	Kind          string
	Name, Mime    string
	Size          int64
	Width, Height int
	DurationMS    *int
	Transcript    *string
	URL           *string
	LinkTitle     *string
	HasThumb      bool
}

// IdeaTask is a task started from an idea, as the card shows it.
type IdeaTask struct {
	ID       int64   `json:"id"`
	Title    string  `json:"title"`
	Status   string  `json:"status"`
	PRURL    *string `json:"pr_url"`
	PRNumber *int    `json:"pr_number"`
}

// Idea is a card of the board.
type Idea struct {
	ID          int64             `json:"id"`
	ProjectID   *string           `json:"project_id"`
	Title       string            `json:"title"`
	Body        string            `json:"body"`
	Column      string            `json:"column"`
	Position    float64           `json:"position"`
	Tags        []string          `json:"tags"`
	AutoNote    *string           `json:"auto_note"`
	Source      string            `json:"source"`
	ArchivedAt  *time.Time        `json:"archived_at"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
	Attachments []*IdeaAttachment `json:"attachments"`
	VoiceNotes  []*IdeaAttachment `json:"voice_notes"`
	// LinkedTaskIDs and LinkedTasks are the tasks started from it, oldest first.
	LinkedTaskIDs []int64    `json:"linked_task_ids"`
	LinkedTasks   []IdeaTask `json:"linked_tasks"`
	// Failed: the latest linked task failed (the card's ✕ stamp). Derived
	// from the task, so it is right even for changes the server missed.
	Failed bool `json:"failed"`
	// Teams: who created it (nil in single-owner mode).
	CreatedByID *int64   `json:"-"`
	CreatedBy   *UserRef `json:"created_by"`
}

// IdeaFields are the editable fields of a new idea.
type IdeaFields struct {
	ProjectID *string
	Title     string
	Body      string
	Column    string
	Tags      []string
	Source    string
}

// IdeaPatch changes an idea; nil fields stay. ProjectID "" clears it.
type IdeaPatch struct {
	ProjectID *string
	Title     *string
	Body      *string
	Tags      *[]string
}

// IdeaFilter selects ideas. Archived lists the archive instead of the board.
type IdeaFilter struct {
	Archived  bool
	ProjectID string
	Tag       string
	Query     string
	Limit     int
}

const ideaCols = `id, project_id, title, body, column_id, position, tags, auto_note, source, archived_at, created_at, updated_at,
	created_by, (SELECT u.name FROM users u WHERE u.id = ideas.created_by)`

func scanIdea(row interface{ Scan(...any) error }) (*Idea, error) {
	var i Idea
	var creator *string
	if err := row.Scan(&i.ID, &i.ProjectID, &i.Title, &i.Body, &i.Column, &i.Position, &i.Tags, &i.AutoNote, &i.Source,
		&i.ArchivedAt, &i.CreatedAt, &i.UpdatedAt, &i.CreatedByID, &creator); err != nil {
		return nil, err
	}
	if i.CreatedByID != nil && creator != nil {
		i.CreatedBy = &UserRef{ID: *i.CreatedByID, Name: *creator, Initials: Initials(*creator)}
	}
	if i.Tags == nil {
		i.Tags = []string{}
	}
	i.Attachments, i.VoiceNotes, i.LinkedTaskIDs, i.LinkedTasks = []*IdeaAttachment{}, []*IdeaAttachment{}, []int64{}, []IdeaTask{}
	return &i, nil
}

const ideaAttachmentCols = `id, idea_id, kind, original_name, mime, size_bytes, width, height, duration_ms, transcript, url, link_title, has_thumb, created_at`

func scanIdeaAttachment(row interface{ Scan(...any) error }) (*IdeaAttachment, error) {
	var a IdeaAttachment
	if err := row.Scan(&a.ID, &a.IdeaID, &a.Kind, &a.Name, &a.Mime, &a.Size, &a.Width, &a.Height, &a.DurationMS, &a.Transcript,
		&a.URL, &a.LinkTitle, &a.HasThumb, &a.CreatedAt); err != nil {
		return nil, err
	}
	return &a, nil
}

// cleanTags trims, lower-cases and de-duplicates tags (empty ones dropped).
func cleanTags(tags []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, t := range tags {
		t = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(t), "#")))
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}

// positionStep is the gap between cards placed at an end of a column.
const positionStep = 1024

// minGap is the smallest gap between neighbours before a column is renumbered.
const minGap = 1e-9

// topPositionTx is a position above every card of the column.
func topPositionTx(ctx context.Context, tx q, column string) (float64, error) {
	var lo *float64
	if err := tx.QueryRow(ctx, `SELECT min(position) FROM ideas WHERE column_id = $1 AND archived_at IS NULL`, column).Scan(&lo); err != nil {
		return 0, err
	}
	if lo == nil {
		return 0, nil
	}
	return *lo - positionStep, nil
}

// CreateIdea inserts an idea at the top of its column with its attachments;
// place writes their files inside the transaction (nil: no files).
func (s *Store) CreateIdea(ctx context.Context, f IdeaFields, items []NewIdeaAttachment, place func(ideaID int64, rows []*IdeaAttachment) error) (*Idea, error) {
	if f.Column == "" {
		f.Column = IdeaInbox
	}
	if f.Source == "" {
		f.Source = "web"
	}
	if f.ProjectID != nil && *f.ProjectID == "" {
		f.ProjectID = nil
	}
	var id int64
	err := s.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('ideas_order'))`); err != nil {
			return err
		}
		pos, err := topPositionTx(ctx, tx, f.Column)
		if err != nil {
			return err
		}
		err = tx.QueryRow(ctx, `INSERT INTO ideas (project_id, title, body, column_id, position, tags, source)
			VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`, f.ProjectID, f.Title, f.Body, f.Column, pos, cleanTags(f.Tags), f.Source).Scan(&id)
		if isFKViolation(err) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		_, err = addIdeaAttachmentsTx(ctx, tx, id, items, place)
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.GetIdea(ctx, id)
}

func addIdeaAttachmentsTx(ctx context.Context, tx pgx.Tx, ideaID int64, items []NewIdeaAttachment, place func(int64, []*IdeaAttachment) error) ([]*IdeaAttachment, error) {
	out := []*IdeaAttachment{}
	for _, it := range items {
		a, err := scanIdeaAttachment(tx.QueryRow(ctx, `INSERT INTO idea_attachments
			(idea_id, kind, original_name, mime, size_bytes, width, height, duration_ms, transcript, url, link_title, has_thumb)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12) RETURNING `+ideaAttachmentCols,
			ideaID, it.Kind, it.Name, it.Mime, it.Size, it.Width, it.Height, it.DurationMS, it.Transcript, it.URL, it.LinkTitle, it.HasThumb))
		if err != nil {
			return nil, notFound(err)
		}
		out = append(out, a)
	}
	if place != nil && len(out) > 0 {
		if err := place(ideaID, out); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// AddIdeaAttachments adds attachments to an idea (see CreateIdea for place)
// and touches its updated_at.
func (s *Store) AddIdeaAttachments(ctx context.Context, ideaID int64, items []NewIdeaAttachment, place func(int64, []*IdeaAttachment) error) ([]*IdeaAttachment, error) {
	var out []*IdeaAttachment
	err := s.Tx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE ideas SET updated_at = now() WHERE id = $1`, ideaID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		out, err = addIdeaAttachmentsTx(ctx, tx, ideaID, items, place)
		return err
	})
	return out, err
}

// GetIdeaAttachment returns one attachment of an idea.
func (s *Store) GetIdeaAttachment(ctx context.Context, ideaID, id int64) (*IdeaAttachment, error) {
	a, err := scanIdeaAttachment(s.Pool.QueryRow(ctx, `SELECT `+ideaAttachmentCols+` FROM idea_attachments WHERE id = $1 AND idea_id = $2`, id, ideaID))
	return a, notFound(err)
}

// IdeaAttachments are an idea's attachments, oldest first (empty for an unknown idea).
func (s *Store) IdeaAttachments(ctx context.Context, ideaID int64) ([]*IdeaAttachment, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+ideaAttachmentCols+` FROM idea_attachments WHERE idea_id = $1 ORDER BY id`, ideaID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (*IdeaAttachment, error) { return scanIdeaAttachment(r) })
}

// DeleteIdeaAttachment removes an attachment row (the caller removes its files).
func (s *Store) DeleteIdeaAttachment(ctx context.Context, ideaID, id int64) error {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM idea_attachments WHERE id = $1 AND idea_id = $2`, id, ideaID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	_, err = s.Pool.Exec(ctx, `UPDATE ideas SET updated_at = now() WHERE id = $1`, ideaID)
	return err
}

// GetIdea returns an idea with its attachments and linked tasks.
func (s *Store) GetIdea(ctx context.Context, id int64) (*Idea, error) {
	i, err := scanIdea(s.Pool.QueryRow(ctx, `SELECT `+ideaCols+` FROM ideas WHERE id = $1`, id))
	if err != nil {
		return nil, notFound(err)
	}
	if err := s.fillIdeas(ctx, []*Idea{i}); err != nil {
		return nil, err
	}
	return i, nil
}

// ListIdeas returns the board (by column and position) or the archive
// (newest archived first), with attachments and linked tasks.
func (s *Store) ListIdeas(ctx context.Context, f IdeaFilter) ([]*Idea, error) {
	if f.Limit <= 0 || f.Limit > 1000 {
		f.Limit = 1000
	}
	where := []string{"archived_at IS NULL"}
	order := "column_id, position, id"
	if f.Archived {
		where[0], order = "archived_at IS NOT NULL", "archived_at DESC, id DESC"
	}
	args := []any{}
	if f.ProjectID != "" {
		args = append(args, f.ProjectID)
		where = append(where, fmt.Sprintf("project_id = $%d", len(args)))
	}
	if t := cleanTags([]string{f.Tag}); len(t) == 1 {
		args = append(args, t[0])
		where = append(where, fmt.Sprintf("$%d = ANY(tags)", len(args)))
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		args = append(args, "%"+strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(q)+"%")
		n := len(args)
		where = append(where, fmt.Sprintf(`(title ILIKE $%d OR body ILIKE $%d OR EXISTS (SELECT 1 FROM idea_attachments a
			WHERE a.idea_id = ideas.id AND (a.transcript ILIKE $%d OR a.link_title ILIKE $%d OR a.url ILIKE $%d OR a.original_name ILIKE $%d)))`, n, n, n, n, n, n))
	}
	args = append(args, f.Limit)
	rows, err := s.Pool.Query(ctx, `SELECT `+ideaCols+` FROM ideas WHERE `+strings.Join(where, " AND ")+
		` ORDER BY `+order+fmt.Sprintf(` LIMIT $%d`, len(args)), args...)
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (*Idea, error) { return scanIdea(r) })
	if err != nil {
		return nil, err
	}
	return list, s.fillIdeas(ctx, list)
}

// fillIdeas loads the attachments and linked tasks of ideas.
func (s *Store) fillIdeas(ctx context.Context, list []*Idea) error {
	if len(list) == 0 {
		return nil
	}
	byID := map[int64]*Idea{}
	ids := make([]int64, 0, len(list))
	for _, i := range list {
		byID[i.ID] = i
		ids = append(ids, i.ID)
	}
	rows, err := s.Pool.Query(ctx, `SELECT `+ideaAttachmentCols+` FROM idea_attachments WHERE idea_id = ANY($1) ORDER BY id`, ids)
	if err != nil {
		return err
	}
	atts, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (*IdeaAttachment, error) { return scanIdeaAttachment(r) })
	if err != nil {
		return err
	}
	for _, a := range atts {
		i := byID[a.IdeaID]
		if a.Kind == IdeaVoice {
			i.VoiceNotes = append(i.VoiceNotes, a)
		} else {
			i.Attachments = append(i.Attachments, a)
		}
	}
	rows, err = s.Pool.Query(ctx, `SELECT it.idea_id, t.id, t.title, t.status, t.pr_url, t.pr_number
		FROM idea_tasks it JOIN tasks t ON t.id = it.task_id WHERE it.idea_id = ANY($1) ORDER BY it.created_at, t.id`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var ideaID int64
		var t IdeaTask
		if err := rows.Scan(&ideaID, &t.ID, &t.Title, &t.Status, &t.PRURL, &t.PRNumber); err != nil {
			return err
		}
		i := byID[ideaID]
		i.LinkedTaskIDs = append(i.LinkedTaskIDs, t.ID)
		i.LinkedTasks = append(i.LinkedTasks, t)
		i.Failed = t.Status == "failed"
	}
	return rows.Err()
}

// UpdateIdea changes an idea's fields.
func (s *Store) UpdateIdea(ctx context.Context, id int64, p IdeaPatch) (*Idea, error) {
	sets := []string{"updated_at = now()"}
	args := []any{id}
	add := func(col string, v any) {
		args = append(args, v)
		sets = append(sets, fmt.Sprintf("%s = $%d", col, len(args)))
	}
	if p.ProjectID != nil {
		var v *string
		if *p.ProjectID != "" {
			v = p.ProjectID
		}
		add("project_id", v)
	}
	if p.Title != nil {
		add("title", *p.Title)
	}
	if p.Body != nil {
		add("body", *p.Body)
	}
	if p.Tags != nil {
		add("tags", cleanTags(*p.Tags))
	}
	tag, err := s.Pool.Exec(ctx, `UPDATE ideas SET `+strings.Join(sets, ", ")+` WHERE id = $1`, args...)
	if isFKViolation(err) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrNotFound
	}
	return s.GetIdea(ctx, id)
}

// MoveIdea puts an idea into column right after the card afterID (nil: at
// the top). Its position is the midpoint of its new neighbours; when they are
// closer than minGap the column is renumbered first. The owner's move
// clears the auto-move note.
func (s *Store) MoveIdea(ctx context.Context, id int64, column string, afterID *int64) (*Idea, error) {
	err := s.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('ideas_order'))`); err != nil {
			return err
		}
		var archived *time.Time
		if err := tx.QueryRow(ctx, `SELECT archived_at FROM ideas WHERE id = $1`, id).Scan(&archived); err != nil {
			return notFound(err)
		}
		if archived != nil {
			return fmt.Errorf("%w: the idea is archived", ErrConflict)
		}
		pos, err := slotTx(ctx, tx, id, column, afterID)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE ideas SET column_id = $2, position = $3, auto_note = NULL, updated_at = now() WHERE id = $1`, id, column, pos)
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.GetIdea(ctx, id)
}

// slotTx is the position for card id in column right after afterID (nil: top).
func slotTx(ctx context.Context, tx pgx.Tx, id int64, column string, afterID *int64) (float64, error) {
	if afterID != nil && *afterID == id {
		return 0, fmt.Errorf("%w: a card cannot follow itself", ErrConflict)
	}
	for attempt := 0; attempt < 2; attempt++ {
		var lo, hi *float64
		if afterID != nil {
			var p float64
			err := tx.QueryRow(ctx, `SELECT position FROM ideas WHERE id = $1 AND column_id = $2 AND archived_at IS NULL`, *afterID, column).Scan(&p)
			if errors.Is(err, pgx.ErrNoRows) {
				return 0, fmt.Errorf("%w: after_id is not a card of that column", ErrNotFound)
			}
			if err != nil {
				return 0, err
			}
			lo = &p
			err = tx.QueryRow(ctx, `SELECT min(position) FROM ideas WHERE column_id = $1 AND archived_at IS NULL AND id <> $2 AND position > $3`,
				column, id, p).Scan(&hi)
			if err != nil {
				return 0, err
			}
		} else if err := tx.QueryRow(ctx, `SELECT min(position) FROM ideas WHERE column_id = $1 AND archived_at IS NULL AND id <> $2`,
			column, id).Scan(&hi); err != nil {
			return 0, err
		}
		switch {
		case lo == nil && hi == nil:
			return 0, nil
		case lo == nil:
			return *hi - positionStep, nil
		case hi == nil:
			return *lo + positionStep, nil
		case *hi-*lo >= minGap:
			return *lo + (*hi-*lo)/2, nil
		}
		// The neighbours are too close for a midpoint: spread the column out and look again.
		if err := renumberTx(ctx, tx, column); err != nil {
			return 0, err
		}
	}
	return 0, errors.New("ideas: no room in the column after renumbering")
}

// renumberTx spreads a column's cards positionStep apart, keeping their order.
func renumberTx(ctx context.Context, tx pgx.Tx, column string) error {
	_, err := tx.Exec(ctx, `UPDATE ideas SET position = r.n * $2 FROM (
		SELECT id, row_number() OVER (ORDER BY position, id) AS n FROM ideas WHERE column_id = $1 AND archived_at IS NULL
	) r WHERE ideas.id = r.id`, column, float64(positionStep))
	return err
}

// MoveIdeaAuto moves an idea to the top of column with a note saying why
// (an automatic move). moved is false when it already was in that column.
func (s *Store) MoveIdeaAuto(ctx context.Context, id int64, column, note string) (idea *Idea, moved bool, err error) {
	err = s.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('ideas_order'))`); err != nil {
			return err
		}
		var cur string
		var archived *time.Time
		if err := tx.QueryRow(ctx, `SELECT column_id, archived_at FROM ideas WHERE id = $1`, id).Scan(&cur, &archived); err != nil {
			return notFound(err)
		}
		if cur == column || archived != nil {
			return nil
		}
		pos, err := topPositionTx(ctx, tx, column)
		if err != nil {
			return err
		}
		moved = true
		_, err = tx.Exec(ctx, `UPDATE ideas SET column_id = $2, position = $3, auto_note = $4, updated_at = now() WHERE id = $1`, id, column, pos, note)
		return err
	})
	if err != nil {
		return nil, false, err
	}
	idea, err = s.GetIdea(ctx, id)
	return idea, moved, err
}

// SetIdeaArchived archives an idea or brings it back (to the top of its column).
func (s *Store) SetIdeaArchived(ctx context.Context, id int64, archived bool) (*Idea, error) {
	err := s.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('ideas_order'))`); err != nil {
			return err
		}
		var col string
		if err := tx.QueryRow(ctx, `SELECT column_id FROM ideas WHERE id = $1`, id).Scan(&col); err != nil {
			return notFound(err)
		}
		if archived {
			_, err := tx.Exec(ctx, `UPDATE ideas SET archived_at = coalesce(archived_at, now()), updated_at = now() WHERE id = $1`, id)
			return err
		}
		pos, err := topPositionTx(ctx, tx, col)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE ideas SET archived_at = NULL, position = $2, updated_at = now() WHERE id = $1 AND archived_at IS NOT NULL`, id, pos)
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.GetIdea(ctx, id)
}

// ErrIdeaNotArchived: only archived ideas can be deleted.
var ErrIdeaNotArchived = fmt.Errorf("%w: archive the idea before deleting it", ErrConflict)

// DeleteIdea removes an archived idea and its rows (the caller removes its files).
func (s *Store) DeleteIdea(ctx context.Context, id int64) error {
	var archived *time.Time
	if err := s.Pool.QueryRow(ctx, `SELECT archived_at FROM ideas WHERE id = $1`, id).Scan(&archived); err != nil {
		return notFound(err)
	}
	if archived == nil {
		return ErrIdeaNotArchived
	}
	_, err := s.Pool.Exec(ctx, `DELETE FROM ideas WHERE id = $1`, id)
	return err
}

// IdeaExists reports whether an idea row exists.
func (s *Store) IdeaExists(ctx context.Context, id int64) (bool, error) {
	var ok bool
	err := s.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM ideas WHERE id = $1)`, id).Scan(&ok)
	return ok, err
}

// CountIdeasInColumn counts the cards (archived too) in a column.
func (s *Store) CountIdeasInColumn(ctx context.Context, column string) (int, error) {
	var n int
	err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM ideas WHERE column_id = $1`, column).Scan(&n)
	return n, err
}

// LinkIdeaTask records that task was started from idea.
func (s *Store) LinkIdeaTask(ctx context.Context, ideaID, taskID int64) error {
	_, err := s.Pool.Exec(ctx, `INSERT INTO idea_tasks (idea_id, task_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, ideaID, taskID)
	if isFKViolation(err) {
		return ErrNotFound
	}
	return err
}

// IdeaIDsForTask lists the ideas a task was started from.
func (s *Store) IdeaIDsForTask(ctx context.Context, taskID int64) ([]int64, error) {
	rows, err := s.Pool.Query(ctx, `SELECT idea_id FROM idea_tasks WHERE task_id = $1 ORDER BY idea_id`, taskID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[int64])
}
