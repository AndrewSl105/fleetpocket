package attachments

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"mime"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/andrewsl105/fleetpocket/server/internal/store"
)

// IdeasDir is the folder under Dir that holds idea files:
// <Dir>/ideas/<idea_id>/<id>.<ext>, thumbnails in .thumbs/. SweepAll's task
// pass skips it (not a number); sweepIdeas handles it.
const IdeasDir = "ideas"

// audioExts are the recordings a voice note may be (what MediaRecorder in
// Chrome/Firefox and Safari, and Telegram voice messages produce).
var audioExts = map[string]string{
	"audio/webm": "webm", "audio/ogg": "ogg", "audio/opus": "opus", "audio/mp4": "m4a", "audio/x-m4a": "m4a", "audio/aac": "aac",
	"audio/mpeg": "mp3", "audio/wav": "wav", "audio/x-wav": "wav", "video/webm": "webm",
}

// AudioType returns the base MIME type of a declared recording type
// ("audio/webm;codecs=opus" → "audio/webm") and whether it is one we accept.
// video/webm (what some browsers call an audio-only WebM) becomes audio/webm.
func AudioType(declared string) (string, bool) {
	mt, _, err := mime.ParseMediaType(strings.TrimSpace(declared))
	if err != nil {
		return "", false
	}
	if _, ok := audioExts[mt]; !ok {
		return "", false
	}
	if mt == "video/webm" {
		mt = "audio/webm"
	}
	return mt, true
}

func ideaFileName(a *store.IdeaAttachment) string {
	ext := "bin"
	switch {
	case a.Kind == store.IdeaVoice:
		if e, ok := audioExts[a.Mime]; ok {
			ext = e
		}
	case IsImage(a.Mime):
		ext = Ext(a.Mime)
	default:
		ext = NameExt(a.Name)
	}
	return strconv.FormatInt(a.ID, 10) + "." + ext
}

// IdeaDir is the folder of one idea's files.
func (f *Files) IdeaDir(ideaID int64) string {
	return filepath.Join(f.Dir, IdeasDir, strconv.FormatInt(ideaID, 10))
}

// IdeaPath is where an idea attachment's file lives (links have none).
func (f *Files) IdeaPath(a *store.IdeaAttachment) string {
	return filepath.Join(f.IdeaDir(a.IdeaID), ideaFileName(a))
}

// IdeaThumbPath is where an idea attachment's thumbnail lives.
func (f *Files) IdeaThumbPath(a *store.IdeaAttachment) string {
	return filepath.Join(f.IdeaDir(a.IdeaID), thumbsDir, strconv.FormatInt(a.ID, 10)+".jpg")
}

// IdeaFile is what to write for one idea attachment row: its file (nil for
// a link) and a JPEG thumbnail (nil when there is none).
type IdeaFile struct {
	Data  []byte
	Thumb []byte
}

// PlaceIdea returns the place function for store.CreateIdea and
// store.AddIdeaAttachments: it writes files[i] under the id of row i.
func (f *Files) PlaceIdea(files []IdeaFile) func(ideaID int64, rows []*store.IdeaAttachment) error {
	return func(ideaID int64, rows []*store.IdeaAttachment) error {
		if len(rows) != len(files) {
			return fmt.Errorf("attachments: %d rows for %d idea files", len(rows), len(files))
		}
		if err := os.MkdirAll(filepath.Join(f.IdeaDir(ideaID), thumbsDir), 0o750); err != nil {
			return err
		}
		for i, a := range rows {
			if files[i].Data != nil {
				if err := writeFile(f.IdeaPath(a), files[i].Data); err != nil {
					return err
				}
			}
			if files[i].Thumb != nil {
				if err := writeFile(f.IdeaThumbPath(a), files[i].Thumb); err != nil {
					return err
				}
			}
		}
		return nil
	}
}

// RemoveIdeaAttachment removes an attachment's file and thumbnail.
func (f *Files) RemoveIdeaAttachment(a *store.IdeaAttachment) {
	_ = os.Remove(f.IdeaPath(a))
	_ = os.Remove(f.IdeaThumbPath(a))
}

// RemoveIdea removes all files of an idea.
func (f *Files) RemoveIdea(ideaID int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return os.RemoveAll(f.IdeaDir(ideaID))
}

// sweepIdeas removes idea folders without an idea row and files without an
// attachment row (leftovers of failed uploads). Called with f.mu held.
func (f *Files) sweepIdeas(ctx context.Context, st *store.Store) error {
	root := filepath.Join(f.Dir, IdeasDir)
	entries, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var errs []error
	for _, e := range entries {
		id, err := strconv.ParseInt(e.Name(), 10, 64)
		if !e.IsDir() || err != nil || id <= 0 {
			continue
		}
		dir := filepath.Join(root, e.Name())
		ok, err := st.IdeaExists(ctx, id)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if !ok {
			errs = append(errs, os.RemoveAll(dir))
			continue
		}
		rows, err := st.IdeaAttachments(ctx, id)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		keep := map[string]bool{}
		for _, a := range rows {
			keep[f.IdeaPath(a)] = true
			keep[f.IdeaThumbPath(a)] = true
		}
		for _, d := range []string{dir, filepath.Join(dir, thumbsDir)} {
			files, err := os.ReadDir(d)
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				errs = append(errs, err)
			}
			for _, fe := range files {
				p := filepath.Join(d, fe.Name())
				if fe.IsDir() || keep[p] {
					continue
				}
				if err := os.Remove(p); err != nil {
					errs = append(errs, err)
				}
			}
		}
	}
	return errors.Join(errs...)
}
