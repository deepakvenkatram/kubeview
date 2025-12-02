package main

import "github.com/charmbracelet/lipgloss"

// Styles defines the styles for the UI.
type Styles struct {
	Base,
	HeaderText,
	Header,
	Footer,
	TableHeader,
	Row,
	SelectedItem,
	Success,
	Warning,
	Error,
	Muted,
	ChartBar,
	ChartText,
	Title,
	Bold,
	ChartTitle,
	Tab,
	ActiveTab lipgloss.Style
}

// DefaultStyles returns a new set of default styles.
func DefaultStyles() Styles {
	s := Styles{}

	s.Base = lipgloss.NewStyle().
		Padding(1, 2)

	s.HeaderText = lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#FFD700")) // Gold

	s.Header = lipgloss.NewStyle().
		Padding(0, 1).
		BorderBottom(true).
		BorderForeground(lipgloss.Color("#5C5CFF")) // Brighter Blue

	s.Footer = s.Header.Copy()

	s.TableHeader = lipgloss.NewStyle().
		Bold(true).
		Padding(0, 1).
		Foreground(lipgloss.Color("#00BFFF")) // DeepSkyBlue

	s.Row = lipgloss.NewStyle().
		Padding(0, 1)

	s.SelectedItem = s.Row.Copy().
		Background(lipgloss.Color("#005FFF")). // Darker Blue
		Foreground(lipgloss.Color("#FFFFFF"))   // White

	s.Success = lipgloss.NewStyle().
		Foreground(lipgloss.Color("35")) // Green

	s.Warning = lipgloss.NewStyle().
		Foreground(lipgloss.Color("214")) // Yellow

	s.Error = lipgloss.NewStyle().
		Foreground(lipgloss.Color("196")) // Red

	s.Muted = lipgloss.NewStyle().
		Foreground(lipgloss.Color("240"))

	s.ChartBar = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#32CD32")) // Lime Green

	s.ChartText = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#FFFFFF"))

	s.Title = lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#00BFFF")).
		Underline(true)

	s.Bold = lipgloss.NewStyle().Bold(true)

	s.ChartTitle = lipgloss.NewStyle().
		Bold(true).
		Padding(0, 1).
		Foreground(lipgloss.Color("#FFD700")) // Gold

	s.Tab = lipgloss.NewStyle().
		Padding(0, 2).
		MarginRight(1).
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("240"))

	s.ActiveTab = s.Tab.Copy().
		Foreground(lipgloss.Color("#FFFFFF")).
		Background(lipgloss.Color("#005FFF")). // Darker Blue
		BorderForeground(lipgloss.Color("#005FFF"))

	return s
}
