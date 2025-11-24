package main

import "github.com/charmbracelet/lipgloss"

type Styles struct {
	Base,
	HeaderText,
	Header,
	Row,
	SelectedRow,
	Success,
	Warning,
	Error,
	Muted lipgloss.Style
}

func defaultStyles() Styles {
	s := Styles{}

	s.Base = lipgloss.NewStyle().
		Padding(1, 2)

	s.HeaderText = lipgloss.NewStyle().
		Bold(true)

	s.Header = lipgloss.NewStyle().
		BorderStyle(lipgloss.NormalBorder()).
		BorderBottom(true).
		Padding(0, 1)

	s.Row = lipgloss.NewStyle().
		Padding(0, 1)

	s.SelectedRow = s.Row.Copy().
		Background(lipgloss.Color("240")).
		Foreground(lipgloss.Color("255"))

	s.Success = lipgloss.NewStyle().
		Foreground(lipgloss.Color("10")) // Green

	s.Warning = lipgloss.NewStyle().
		Foreground(lipgloss.Color("11")) // Yellow

	s.Error = lipgloss.NewStyle().
		Foreground(lipgloss.Color("9")) // Red

	s.Muted = lipgloss.NewStyle().
		Foreground(lipgloss.Color("8")) // Grey

	return s
}
