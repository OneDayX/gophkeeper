package tui

import "github.com/charmbracelet/lipgloss"

// The styles of the TUI. Colors are from the 256-color palette, which every
// modern terminal has; lipgloss drops them where the terminal has none.
var (
	titleStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	labelStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	focusStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
	statusStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("78"))
	errorStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	helpStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	codeStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("214"))
	plainStyle  = lipgloss.NewStyle()
)

// valueStyle returns the style of a value on the detail screen: the OTP code
// stands out.
func valueStyle(label string) lipgloss.Style {
	if label == "Code" {
		return codeStyle
	}

	return plainStyle
}
