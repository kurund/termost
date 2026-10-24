// Package clip reads images from the desktop clipboard via wl-paste or xclip.
package clip

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

var ErrNoImage = errors.New("the clipboard has no image")

var extensions = map[string]string{
	"image/png":  ".png",
	"image/jpeg": ".jpg",
	"image/gif":  ".gif",
	"image/webp": ".webp",
	"image/bmp":  ".bmp",
}

// Image returns the clipboard image and a file extension for it.
func Image(ctx context.Context) ([]byte, string, error) {
	var list, read func(mime string) *exec.Cmd
	switch {
	case os.Getenv("WAYLAND_DISPLAY") != "":
		list = func(string) *exec.Cmd { return exec.CommandContext(ctx, "wl-paste", "--list-types") }
		read = func(mime string) *exec.Cmd {
			return exec.CommandContext(ctx, "wl-paste", "--no-newline", "--type", mime)
		}
	case os.Getenv("DISPLAY") != "":
		list = func(string) *exec.Cmd {
			return exec.CommandContext(ctx, "xclip", "-selection", "clipboard", "-t", "TARGETS", "-o")
		}
		read = func(mime string) *exec.Cmd {
			return exec.CommandContext(ctx, "xclip", "-selection", "clipboard", "-t", mime, "-o")
		}
	default:
		return nil, "", errors.New("no graphical session (neither WAYLAND_DISPLAY nor DISPLAY is set)")
	}

	out, err := list("").Output()
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return nil, "", fmt.Errorf("install wl-clipboard (Wayland) or xclip (X11): %w", err)
		}
		return nil, "", fmt.Errorf("reading clipboard types: %w", err)
	}
	mime := PickImageType(strings.Split(string(out), "\n"))
	if mime == "" {
		return nil, "", ErrNoImage
	}
	data, err := read(mime).Output()
	if err != nil {
		return nil, "", fmt.Errorf("reading clipboard image: %w", err)
	}
	return data, extensions[mime], nil
}

// PickImageType prefers PNG, then any other image type we know an extension for.
func PickImageType(types []string) string {
	best := ""
	for _, t := range types {
		t = strings.TrimSpace(t)
		if _, ok := extensions[t]; !ok {
			continue
		}
		if t == "image/png" {
			return t
		}
		if best == "" {
			best = t
		}
	}
	return best
}

// ErrNoTool means no clipboard program can be used; callers can fall back to
// the terminal's OSC 52 clipboard.
var ErrNoTool = errors.New("no clipboard tool available")

// Copy puts text on the desktop clipboard via wl-copy or xclip.
func Copy(ctx context.Context, text string) error {
	var cmd *exec.Cmd
	switch {
	case os.Getenv("WAYLAND_DISPLAY") != "":
		cmd = exec.CommandContext(ctx, "wl-copy")
	case os.Getenv("DISPLAY") != "":
		cmd = exec.CommandContext(ctx, "xclip", "-selection", "clipboard", "-i")
	default:
		return ErrNoTool
	}
	cmd.Stdin = strings.NewReader(text)
	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return ErrNoTool
		}
		return fmt.Errorf("copying to clipboard: %w", err)
	}
	return nil
}
