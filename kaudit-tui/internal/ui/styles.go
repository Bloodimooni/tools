package ui

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
)

var (
	cAccent = lipgloss.AdaptiveColor{Light: "#5A3FC0", Dark: "#A78BFA"}
	cMuted  = lipgloss.AdaptiveColor{Light: "#6B7280", Dark: "#8B93A1"}
	cOK     = lipgloss.AdaptiveColor{Light: "#15803D", Dark: "#4ADE80"}
	cWarn   = lipgloss.AdaptiveColor{Light: "#B45309", Dark: "#FBBF24"}
	cErr    = lipgloss.AdaptiveColor{Light: "#B91C1C", Dark: "#F87171"}
	cBar    = lipgloss.AdaptiveColor{Light: "#2563EB", Dark: "#60A5FA"}
	cSel    = lipgloss.AdaptiveColor{Light: "#C2410C", Dark: "#FB923C"}

	sTitle  = lipgloss.NewStyle().Bold(true).Foreground(cAccent)
	sMuted  = lipgloss.NewStyle().Foreground(cMuted)
	sBold   = lipgloss.NewStyle().Bold(true)
	sCursor = lipgloss.NewStyle().Bold(true).
		Foreground(lipgloss.AdaptiveColor{Light: "#FFFFFF", Dark: "#FFFFFF"}).
		Background(lipgloss.AdaptiveColor{Light: "#5A3FC0", Dark: "#6D28D9"})
	sTabOn = lipgloss.NewStyle().Bold(true).Padding(0, 1).
		Foreground(lipgloss.AdaptiveColor{Light: "#FFFFFF", Dark: "#1E1B2E"}).Background(cAccent)
	sTabOff   = lipgloss.NewStyle().Padding(0, 1).Foreground(cMuted)
	sHdr      = lipgloss.NewStyle().Foreground(cMuted).Bold(true)
	sHdrOn    = lipgloss.NewStyle().Foreground(cAccent).Bold(true).Underline(true)
	sUser     = lipgloss.NewStyle().Foreground(cAccent)
	sSorted   = lipgloss.NewStyle().Bold(true).Foreground(cAccent)
	sMark     = lipgloss.NewStyle().Foreground(cSel).Bold(true)
	sBar      = lipgloss.NewStyle().Foreground(cBar)
	sBarSel   = lipgloss.NewStyle().Foreground(cSel)
	sBarCur   = lipgloss.NewStyle().Foreground(cErr).Bold(true)
	sKey      = lipgloss.NewStyle().Foreground(cAccent)
	sJSONKey  = lipgloss.NewStyle().Foreground(cBar)
	sStatusOK = lipgloss.NewStyle().Foreground(cOK)
	sWarn     = lipgloss.NewStyle().Foreground(cWarn)
	sErr      = lipgloss.NewStyle().Foreground(cErr).Bold(true)
	sPill     = lipgloss.NewStyle().Foreground(lipgloss.Color("0")).Background(cAccent).Padding(0, 1)
)

// codeStyle colors an HTTP status code: green 2xx, yellow 4xx, red 401/403/5xx.
func codeStyle(code int) lipgloss.Style {
	switch {
	case code == 0:
		return sMuted
	case code == 401 || code == 403 || code >= 500:
		return sErr
	case code >= 400:
		return sWarn
	case code >= 300:
		return sMuted
	}
	return sStatusOK
}

func codeStr(code int) string {
	if code == 0 {
		return "---"
	}
	return fmt.Sprint(code)
}

func fn(s lipgloss.Style) func(string) string { return func(x string) string { return s.Render(x) } }

// verbStyle: reads are plain, writes amber, deletes red.
func verbStyle(v string) lipgloss.Style {
	switch v {
	case "delete", "deletecollection":
		return sErr
	case "create", "update", "patch", "impersonate", "escalate", "bind":
		return sWarn.Bold(true)
	}
	return sBold
}
