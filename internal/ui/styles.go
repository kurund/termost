package ui

import (
	"hash/fnv"

	"charm.land/lipgloss/v2"
	"github.com/mattermost/mattermost/server/public/model"
)

const (
	sidebarWidth = 30
	narrowWidth  = 70
	minWidth     = 40
	minHeight    = 12
)

var (
	faint       = lipgloss.NewStyle().Faint(true)
	bold        = lipgloss.NewStyle().Bold(true)
	selected    = lipgloss.NewStyle().Reverse(true)
	cursorRow   = lipgloss.NewStyle().Underline(true)
	errorStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	online      = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	mentionTag  = lipgloss.NewStyle().Foreground(lipgloss.Color("0")).Background(lipgloss.Color("3")).Bold(true)
	ownName     = lipgloss.NewStyle().Foreground(lipgloss.Color("4")).Bold(true)
	unreadStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true)
	readStyle   = lipgloss.NewStyle().Faint(true)
	matchStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("3")).Bold(true)
	mutedStyle  = lipgloss.NewStyle().Faint(true).Italic(true)
	awayStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	dndStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	sidebarBox  = lipgloss.NewStyle().BorderStyle(lipgloss.NormalBorder()).BorderRight(true).BorderForeground(lipgloss.Color("8"))
	headerBox   = lipgloss.NewStyle().BorderStyle(lipgloss.NormalBorder()).BorderBottom(true).BorderForeground(lipgloss.Color("8"))
	inputBox    = lipgloss.NewStyle().BorderStyle(lipgloss.NormalBorder()).BorderTop(true).BorderForeground(lipgloss.Color("8"))
)

var selBar = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Render("▌")

var nameColors = []string{"1", "2", "3", "5", "6", "9", "10", "11", "13", "14"}

func userStyle(username string) lipgloss.Style {
	h := fnv.New32a()
	_, _ = h.Write([]byte(username))
	return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(nameColors[h.Sum32()%uint32(len(nameColors))]))
}

func presence(status string) string {
	switch status {
	case model.StatusOnline:
		return online.Render("●") + " "
	case model.StatusAway:
		return awayStyle.Render("◐") + " "
	case model.StatusDnd:
		return dndStyle.Render("⊖") + " "
	case model.StatusOffline:
		return faint.Render("○") + " "
	}
	return faint.Render("@") + " "
}
