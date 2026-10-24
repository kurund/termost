package ui

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/mattermost/mattermost/server/public/model"

	"github.com/kurund/termost/internal/clip"
	"github.com/kurund/termost/internal/mm"
)

type noticeMsg struct {
	text string
	err  error
}

var (
	trailingPasteRe  = regexp.MustCompile(`(?s)^(.*\S)\s+/paste\s*$`)
	trailingUploadRe = regexp.MustCompile(`(?s)^(.*?\S)\s+/upload\s+(.+?)\s*$`)
)

// slash handles termost's own /upload and /paste, also at the end of a
// message, "//" to send a literal slash, and other "/" input as a server command.
func (m *Model) slash(text string) (tea.Cmd, string, bool) {
	trimmed := strings.TrimSpace(text)
	switch {
	case strings.HasPrefix(trimmed, "//"):
		return nil, text[strings.Index(text, "/")+1:], false
	case trimmed == "/upload" || strings.HasPrefix(trimmed, "/upload "):
		return m.upload(strings.TrimSpace(strings.TrimPrefix(trimmed, "/upload"))), "", true
	case trimmed == "/paste":
		return m.paste("", ""), "", true
	case trimmed == "/search" || strings.HasPrefix(trimmed, "/search "):
		return m.openSearch(strings.TrimSpace(strings.TrimPrefix(trimmed, "/search"))), "", true
	case strings.HasPrefix(trimmed, "/"):
		return m.command(trimmed), "", true
	}
	if match := trailingPasteRe.FindStringSubmatch(text); match != nil {
		return m.paste(match[1], text), "", true
	}
	if match := trailingUploadRe.FindStringSubmatch(text); match != nil {
		if paths, ok := existingFiles(match[2]); ok {
			return m.uploadFiles(paths, match[1], text), "", true
		}
	}
	return nil, text, false
}

func (m *Model) target() (chanRef, string) {
	if m.thread != nil {
		return m.thread.ref, m.thread.rootID
	}
	return m.current, ""
}

func (m *Model) upload(arg string) tea.Cmd {
	if arg == "" {
		return func() tea.Msg { return noticeMsg{err: errors.New("usage: /upload <path> [path...]")} }
	}
	paths, err := uploadPaths(arg)
	if err != nil {
		return func() tea.Msg { return noticeMsg{err: err} }
	}
	return m.uploadFiles(paths, "", "")
}

// uploadFiles posts the files with an optional message; draft is the input to
// restore if that fails.
func (m *Model) uploadFiles(paths []string, message, draft string) tea.Cmd {
	ref, rootID := m.target()
	fail := uploadFailure(ref, draft)
	if len(paths) > maxFilesPerPost {
		return func() tea.Msg { return fail(fmt.Errorf("at most %d files per message", maxFilesPerPost)) }
	}
	sess := m.byName[ref.server]
	label := filesLabel(paths)
	m.notice = "Uploading " + label + "…"
	return func() tea.Msg {
		files := make([]mm.File, 0, len(paths))
		for _, p := range paths {
			data, err := os.ReadFile(p)
			if err != nil {
				return fail(err)
			}
			files = append(files, mm.File{Name: filepath.Base(p), Data: data})
		}
		if err := sess.Upload(m.ctx, ref.channel, rootID, message, files...); err != nil {
			return fail(fmt.Errorf("upload failed: %w", err))
		}
		return noticeMsg{text: "Uploaded " + label}
	}
}

func uploadFailure(ref chanRef, draft string) func(error) tea.Msg {
	return func(err error) tea.Msg {
		if draft != "" {
			return sendFailedMsg{ref: ref, text: draft, err: err}
		}
		return noticeMsg{err: err}
	}
}

const maxFilesPerPost = 10

func filesLabel(paths []string) string {
	if len(paths) == 1 {
		return filepath.Base(paths[0])
	}
	return fmt.Sprintf("%d files", len(paths))
}

// uploadPaths reads an /upload argument: a plain path, or what terminals type
// when files are dropped on them (shell-escaped or quoted paths, file:// URIs).
func uploadPaths(arg string) ([]string, error) {
	if p := expandHome(arg); isFile(p) {
		return []string{p}, nil
	}
	words, err := shellWords(arg)
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(words))
	for _, w := range words {
		if strings.HasPrefix(w, "file://") {
			u, err := url.Parse(w)
			if err != nil {
				return nil, err
			}
			w = u.Path
		}
		paths = append(paths, expandHome(w))
	}
	return paths, nil
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// droppedFiles reports whether pasted text is only absolute paths to existing
// files, as when files are dragged onto the terminal.
func droppedFiles(text string) ([]string, bool) {
	paths, ok := existingFiles(text)
	if !ok {
		return nil, false
	}
	for _, p := range paths {
		if !filepath.IsAbs(p) {
			return nil, false
		}
	}
	return paths, true
}

func existingFiles(arg string) ([]string, bool) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return nil, false
	}
	paths, err := uploadPaths(arg)
	if err != nil || len(paths) == 0 {
		return nil, false
	}
	for _, p := range paths {
		if !isFile(p) {
			return nil, false
		}
	}
	return paths, true
}

// shellWords splits s like a POSIX shell, honouring backslashes and quotes.
func shellWords(s string) ([]string, error) {
	var words []string
	var b strings.Builder
	inWord, escaped := false, false
	var quote rune
	runes := []rune(s)
	for i, r := range runes {
		switch {
		case escaped:
			b.WriteRune(r)
			escaped = false
		case quote == '\'':
			if r == '\'' {
				quote = 0
			} else {
				b.WriteRune(r)
			}
		case quote == '"':
			switch {
			case r == '"':
				quote = 0
			case r == '\\' && i+1 < len(runes) && strings.ContainsRune("$`\"\\\n", runes[i+1]):
				escaped = true
			default:
				b.WriteRune(r)
			}
		case r == '\\':
			escaped, inWord = true, true
		case r == '\'' || r == '"':
			quote, inWord = r, true
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			if inWord {
				words = append(words, b.String())
				b.Reset()
				inWord = false
			}
		default:
			b.WriteRune(r)
			inWord = true
		}
	}
	if quote != 0 || escaped {
		return nil, errors.New("unterminated quote or escape in path")
	}
	if inWord {
		words = append(words, b.String())
	}
	return words, nil
}

func (m *Model) paste(message, draft string) tea.Cmd {
	ref, rootID := m.target()
	fail := uploadFailure(ref, draft)
	sess := m.byName[ref.server]
	m.notice = "Reading clipboard…"
	return func() tea.Msg {
		data, ext, err := clip.Image(m.ctx)
		if err != nil {
			return fail(err)
		}
		name := "pasted-" + time.Now().Format("20060102-150405") + ext
		if err := sess.Upload(m.ctx, ref.channel, rootID, message, mm.File{Name: name, Data: data}); err != nil {
			return fail(fmt.Errorf("upload failed: %w", err))
		}
		return noticeMsg{text: "Uploaded " + name}
	}
}

func (m *Model) command(text string) tea.Cmd {
	ref := m.current
	st, sess := m.servers[ref.server], m.byName[ref.server]
	teamID := ""
	if ch := st.Channels[ref.channel]; ch != nil {
		teamID = ch.TeamId
	}
	return func() tea.Msg {
		out, err := sess.Command(m.ctx, ref.channel, teamID, text)
		if err != nil {
			return noticeMsg{err: fmt.Errorf("%s: %w", strings.Fields(text)[0], err)}
		}
		return noticeMsg{text: out}
	}
}

// openAttachments downloads a post's files to the cache and opens them.
func (m *Model) openAttachments(post *model.Post) tea.Cmd {
	infos := fileInfos(post)
	if len(infos) == 0 {
		m.notice = "No attachments on this message"
		return nil
	}
	sess := m.byName[m.current.server]
	m.notice = fmt.Sprintf("Opening %d attachment(s)…", len(infos))
	return func() tea.Msg {
		cache, err := os.UserCacheDir()
		if err != nil {
			return noticeMsg{err: err}
		}
		for _, f := range infos {
			path, err := attachmentPath(cache, f)
			if err != nil {
				return noticeMsg{err: err}
			}
			if _, err := os.Stat(path); err != nil {
				if err := sess.DownloadTo(m.ctx, f.Id, path); err != nil {
					return noticeMsg{err: fmt.Errorf("download %s: %w", f.Name, err)}
				}
			}
			cmd := exec.Command("xdg-open", path)
			if err := cmd.Start(); err != nil {
				return noticeMsg{err: fmt.Errorf("xdg-open: %w", err)}
			}
			go func() { _ = cmd.Wait() }()
		}
		return noticeMsg{text: fmt.Sprintf("Opened %d attachment(s)", len(infos))}
	}
}

// attachmentPath places a download under the cache, refusing file IDs and
// names from the server that would escape its directory.
func attachmentPath(cache string, f *model.FileInfo) (string, error) {
	name := filepath.Base(f.Name)
	if !model.IsValidId(f.Id) || name == "." || name == ".." || name == string(filepath.Separator) {
		return "", fmt.Errorf("refusing attachment with unsafe id %q or name %q", f.Id, f.Name)
	}
	return filepath.Join(cache, "termost", "files", f.Id, name), nil
}

func fileInfos(post *model.Post) []*model.FileInfo {
	if post.Metadata == nil {
		return nil
	}
	return post.Metadata.Files
}

func expandHome(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[1:])
		}
	}
	return path
}

// completePath extends partial to the longest unambiguous path and returns
// the candidate names when more than one matches.
func completePath(partial string) (string, []string) {
	dir, base := filepath.Split(partial)
	readDir := expandHome(dir)
	if readDir == "" {
		readDir = "."
	}
	entries, err := os.ReadDir(readDir)
	if err != nil {
		return partial, nil
	}
	var matches []string
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, base) || (strings.HasPrefix(name, ".") && !strings.HasPrefix(base, ".")) {
			continue
		}
		if e.IsDir() {
			name += "/"
		}
		matches = append(matches, name)
	}
	switch len(matches) {
	case 0:
		return partial, nil
	case 1:
		return dir + matches[0], nil
	}
	common := matches[0]
	for _, m := range matches[1:] {
		for !strings.HasPrefix(m, common) {
			common = common[:len(common)-1]
		}
	}
	return dir + common, matches
}

func (m *Model) completeUpload() bool {
	value := m.input.Value()
	if !strings.HasPrefix(value, "/upload ") {
		return false
	}
	completed, candidates := completePath(strings.TrimPrefix(value, "/upload "))
	m.input.SetValue("/upload " + completed)
	if len(candidates) > 0 {
		m.notice = truncate(strings.Join(candidates, "  "), m.mainWidth())
	} else {
		m.notice = ""
	}
	return true
}
