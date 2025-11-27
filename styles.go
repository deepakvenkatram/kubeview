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
	Muted,
	ChartBar,
	ChartText lipgloss.Style
}

func defaultStyles() Styles {
	s := Styles{}

	s.Base = lipgloss.NewStyle().
		Padding(1, 2).
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#5C5CFF")) // Brighter Blue

	s.HeaderText = lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#FFD700")) // Gold

	s.Header = lipgloss.NewStyle().
		BorderStyle(lipgloss.NormalBorder()).
		BorderBottom(true).
		BorderForeground(lipgloss.Color("#5C5CFF")).
		Padding(0, 1)

	s.Row = lipgloss.NewStyle().
		Padding(0, 1)

	s.SelectedRow = s.Row.Copy().
		Background(lipgloss.Color("#005FFF")). // Darker Blue
		Foreground(lipgloss.Color("#FFFFFF"))   // White

	s.Success = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#32CD32")) // Lime Green

	s.Warning = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#FFA500")) // Orange

	s.Error = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#FF4500")) // Orange Red

	s.Muted = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#808080")) // Grey

	s.ChartBar = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#32CD32")) // Lime Green

	s.ChartText = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#FFFFFF")) // White

	return s
}
