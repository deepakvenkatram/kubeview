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
		Foreground(lipgloss.Color("#FAFAFA"))

	s.Header = lipgloss.NewStyle().
		Padding(0, 1).
		BorderBottom(true).
		BorderForeground(lipgloss.Color("240"))

	s.Footer = s.Header.Copy()

	s.TableHeader = lipgloss.NewStyle().
		Bold(true).
		Padding(0, 1)

	s.Row = lipgloss.NewStyle().
		Padding(0, 1)

	s.SelectedItem = s.Row.Copy().
		Background(lipgloss.Color("240")).
		Foreground(lipgloss.Color("#FAFAFA"))

	s.Success = lipgloss.NewStyle().
		Foreground(lipgloss.Color("35")) // Green

	s.Warning = lipgloss.NewStyle().
		Foreground(lipgloss.Color("214")) // Yellow

	s.Error = lipgloss.NewStyle().
		Foreground(lipgloss.Color("196")) // Red

	s.Muted = lipgloss.NewStyle().
		Foreground(lipgloss.Color("240"))

	s.ChartBar = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#00BFFF")) // DeepSkyBlue

	s.ChartText = lipgloss.NewStyle().
		Foreground(lipgloss.Color("248"))

	s.Title = lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#00BFFF")).
		Underline(true)

	s.Bold = lipgloss.NewStyle().Bold(true)

	s.ChartTitle = lipgloss.NewStyle().
		Bold(true).
		Padding(0, 1)

	s.Tab = lipgloss.NewStyle().
		Padding(0, 2).
		MarginRight(1).
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("240"))

	s.ActiveTab = s.Tab.Copy().
		Foreground(lipgloss.Color("#FAFAFA")).
		Background(lipgloss.Color("#00BFFF")).
		BorderForeground(lipgloss.Color("#00BFFF"))

	return s
}
