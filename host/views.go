package host

import (
	"fmt"
	"strings"
	"github.com/charmbracelet/lipgloss"
)

// RenderHostView renders the host metrics view.
func RenderHostView(headerText, header, row lipgloss.Style, cpu, mem string, disk []DiskUsageStat) string {
	var b strings.Builder
	b.WriteString(headerText.Render("Host Resource Usage") + "\n")
	b.WriteString(fmt.Sprintf("  CPU:\t%s\n", cpu))
	b.WriteString(fmt.Sprintf("  Memory:\t%s\n", mem))
	b.WriteString("\n" + headerText.Render("Disk Usage") + "\n")

	if len(disk) == 0 {
		b.WriteString("  No disk partitions found.\n")
	} else {
		header := header.Render(fmt.Sprintf("%-25s %-15s %-15s %-15s %-10s", "Mountpoint", "Total (GB)", "Used (GB)", "Free (GB)", "Used %"))
		b.WriteString(header + "\n")
		for _, d := range disk {
			totalGB := float64(d.Total) / (1024 * 1024 * 1024)
			usedGB := float64(d.Used) / (1024 * 1024 * 1024)
			freeGB := float64(d.Free) / (1024 * 1024 * 1024)
			line := fmt.Sprintf("%-25s %-15.2f %-15.2f %-15.2f %-10.2f%%", d.Mountpoint, totalGB, usedGB, freeGB, d.UsedPercent)
			b.WriteString(row.Render(line) + "\n")
		}
	}

	return b.String()
}

// RenderHostLogsMenu renders the menu for selecting host log types.
func RenderHostLogsMenu(headerText, row, selected lipgloss.Style, cursor int, logTypes []string) string {
	var b strings.Builder
	b.WriteString(headerText.Render("Select Log Type") + "\n")

	for i, logType := range logTypes {
		style := row
		if cursor == i {
			style = selected
		}
		b.WriteString(style.Render(logType) + "\n")
	}
	return b.String()
}
