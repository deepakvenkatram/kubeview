package host

import (
	"bytes"
	"fmt"
	"os/exec"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/shirou/gopsutil/cpu"
	"github.com/shirou/gopsutil/disk"
	"github.com/shirou/gopsutil/mem"
)

// DiskUsageStat holds information about a single disk partition.
type DiskUsageStat struct {
	Mountpoint  string
	Total       uint64
	Used        uint64
	Free        uint64
	UsedPercent float64
}

// HostMsg is sent when new host metrics are available.
type HostMsg struct {
	CpuUsage    string
	MemoryUsage string
	DiskUsage   []DiskUsageStat
}

// HostLogsMsg is sent when host logs are fetched.
type HostLogsMsg struct{ Logs string }

// errMsg is used to report errors.
type errMsg struct{ err error }

func (e errMsg) Error() string { return e.err.Error() }

// GetHostMetrics fetches CPU, memory, and disk usage.
func GetHostMetrics() tea.Cmd {
	return func() tea.Msg {
		cpuPercentages, err := cpu.Percent(time.Second, false)
		if err != nil {
			return errMsg{err}
		}
		cpuUsage := fmt.Sprintf("%.2f%%", cpuPercentages[0])

		memInfo, err := mem.VirtualMemory()
		if err != nil {
			return errMsg{err}
		}
		memUsage := fmt.Sprintf("%.2f%%", memInfo.UsedPercent)

		partitions, err := disk.Partitions(true)
		if err != nil {
			return errMsg{err}
		}

		var diskUsage []DiskUsageStat
		for _, p := range partitions {
			usage, err := disk.Usage(p.Mountpoint)
			if err != nil {
				continue // Or handle error
			}
			diskUsage = append(diskUsage, DiskUsageStat{
				Mountpoint:  p.Mountpoint,
				Total:       usage.Total,
				Used:        usage.Used,
				Free:        usage.Free,
				UsedPercent: usage.UsedPercent,
			})
		}

		return HostMsg{
			CpuUsage:    cpuUsage,
			MemoryUsage: memUsage,
			DiskUsage:   diskUsage,
		}
	}
}

// GetHostLogs fetches logs from the host using journalctl.
func GetHostLogs(logType string) tea.Cmd {
	return func() tea.Msg {
		if _, err := exec.LookPath("journalctl"); err != nil {
			return errMsg{fmt.Errorf("journalctl not found on this system, this feature is only available on Linux with systemd")}
		}

		var cmd string
		switch logType {
		case "System Logs":
			cmd = "journalctl --no-pager --lines=1000"
		case "Kubelet Logs":
			cmd = "journalctl --no-pager --lines=1000 -u kubelet.service"
		case "Docker Logs":
			cmd = "journalctl --no-pager --lines=1000 -u docker.service"
		default:
			return errMsg{fmt.Errorf("unknown log type: %s", logType)}
		}

		c := exec.Command("bash", "-c", cmd)
		var out bytes.Buffer
		var stderr bytes.Buffer
		c.Stdout = &out
		c.Stderr = &stderr
		err := c.Run()
		if err != nil {
			return errMsg{fmt.Errorf("error running command: %v\n%s", err, stderr.String())}
		}
		return HostLogsMsg{Logs: out.String()}
	}
}
