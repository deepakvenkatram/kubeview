package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/NimbleMarkets/ntcharts/barchart"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/shirou/gopsutil/cpu"
	"github.com/shirou/gopsutil/disk"
	"github.com/shirou/gopsutil/mem"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer/json"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	v1beta1 "k8s.io/metrics/pkg/apis/metrics/v1beta1"
	metrics "k8s.io/metrics/pkg/client/clientset/versioned"
)

var refreshInterval = 5 * time.Second

type viewState int

const (
	viewNodes viewState = iota
	viewPods
	viewPVCs
	viewPVs
	viewDeployments
	viewStatefulSets
	viewDaemonSets
	viewServices
	viewNetworkPolicies
	viewEvents
	viewNamespaces
	viewDetails
	viewLogs
	viewScaling
	viewConfirmDelete
	viewYAML
	viewDashboard // New view state for Dashboard
	viewResourceMenu
	viewHelp
	viewHostDashboard
	viewHostLogs
	viewAppLogs
)

type diskUsageStat struct {
	Mountpoint  string
	Total       uint64
	Used        uint64
	Free        uint64
	UsedPercent float64
}

type model struct {
	view               viewState
	previousView       viewState
	nodes              []v1.Node
	nodeMetrics        map[string]v1beta1.NodeMetrics
	pods               []v1.Pod
	podMetrics         map[string]v1beta1.PodMetrics
	pvcs               []v1.PersistentVolumeClaim
	pvs                []v1.PersistentVolume
	deployments        []appsv1.Deployment
	statefulsets       []appsv1.StatefulSet
	daemonsets         []appsv1.DaemonSet
	services           []v1.Service
	netpols            []networkingv1.NetworkPolicy
	events             []v1.Event
	namespaces         []v1.Namespace
	resourceTypes      []string
	hostLogTypes       []string
	selectedNamespace  string // "" == all
	details            string
	yamlContent        string    // New field for YAML content
	clusterCPUUsage    string    // Aggregated cluster CPU usage
	clusterMemoryUsage string    // Aggregated cluster Memory usage
	topPodsByCPU       []v1.Pod  // Top pods by CPU usage
	topPodsByMemory    []v1.Pod  // Top pods by Memory usage
	topNodesByCPU      []v1.Node // Top nodes by CPU usage
	topNodesByMemory   []v1.Node // Top nodes by Memory usage
	podCPUChart        barchart.Model
	podMemoryChart     barchart.Model
	nodeCPUChart       barchart.Model
	nodeMemoryChart    barchart.Model
	cursor             int
	err                error
	clientset          *kubernetes.Clientset
	metricsClientset   *metrics.Clientset
	styles             Styles
	viewport           viewport.Model
	textInput          textinput.Model
	hostTabs           []string
	activeHostTab      int
	hostCPUChart       barchart.Model
	hostMemoryChart    barchart.Model
	hostDiskUsage      []diskUsageStat
	containers         []string
	ready              bool
}

type tickMsg time.Time
type hostMsg struct {
	cpuUsage    float64
	memoryUsage float64
	diskUsage   []diskUsageStat
}
type appLogsMsg struct{ containers []string }
type containerLogsMsg struct{ logs string }
type hostLogsMsg struct{ logs string }
type logsMsg struct{ logs string }
type scaleMsg struct{}
type podDeletedMsg struct{}
type nodesMsg struct {
	nodes   []v1.Node
	metrics map[string]v1beta1.NodeMetrics
}
type podsMsg struct {
	pods    []v1.Pod
	metrics map[string]v1beta1.PodMetrics
}
type pvcsMsg struct{ pvcs []v1.PersistentVolumeClaim }
type pvsMsg struct{ pvs []v1.PersistentVolume }
type deploymentsMsg struct{ deployments []appsv1.Deployment }
type statefulsetsMsg struct{ statefulsets []appsv1.StatefulSet }
type daemonsetsMsg struct{ daemonsets []appsv1.DaemonSet }
type servicesMsg struct{ services []v1.Service }
type networkPoliciesMsg struct{ policies []networkingv1.NetworkPolicy }
type eventsMsg struct{ events []v1.Event }
type namespacesMsg struct{ namespaces []v1.Namespace }
type errMsg struct{ err error }
type yamlMsg struct{ yaml string } // New message type
type dashboardMsg struct {
	clusterCPUUsage     string
	clusterMemoryUsage  string
	topPodsByCPU        []v1.Pod
	topPodsByMemory     []v1.Pod
	topNodesByCPU       []v1.Node
	topNodesByMemory    []v1.Node
	podCPUChartData     []barchart.BarData
	podMemoryChartData  []barchart.BarData
	nodeCPUChartData    []barchart.BarData
	nodeMemoryChartData []barchart.BarData
}

func (m *model) setView(view viewState) {
	m.previousView = m.view
	m.view = view
	m.cursor = 0
}

func (e errMsg) Error() string { return e.err.Error() }

func doTick() tea.Cmd {
	return tea.Tick(refreshInterval, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func deletePod(clientset *kubernetes.Clientset, namespace, name string) tea.Cmd {
	return func() tea.Msg {
		err := clientset.CoreV1().Pods(namespace).Delete(context.Background(), name, metav1.DeleteOptions{})
		if err != nil {
			return errMsg{err}
		}
		return podDeletedMsg{}
	}
}

func scaleDeployment(clientset *kubernetes.Clientset, namespace, name string, replicas int32) tea.Cmd {
	return func() tea.Msg {
		deployment, err := clientset.AppsV1().Deployments(namespace).Get(context.Background(), name, metav1.GetOptions{})
		if err != nil {
			return errMsg{err}
		}

		deployment.Spec.Replicas = &replicas
		_, err = clientset.AppsV1().Deployments(namespace).Update(context.Background(), deployment, metav1.UpdateOptions{})
		if err != nil {
			return errMsg{err}
		}
		return scaleMsg{}
	}
}

func getHostMetrics() tea.Cmd {
	return func() tea.Msg {
		cpuPercentages, err := cpu.Percent(time.Second, false)
		if err != nil {
			return errMsg{err}
		}
		cpuUsage := cpuPercentages[0]

		memInfo, err := mem.VirtualMemory()
		if err != nil {
			return errMsg{err}
		}
		memUsage := memInfo.UsedPercent

		partitions, err := disk.Partitions(true)
		if err != nil {
			return errMsg{err}
		}

		var diskUsage []diskUsageStat
		for _, p := range partitions {
			usage, err := disk.Usage(p.Mountpoint)
			if err != nil {
				continue // Or handle error
			}
			diskUsage = append(diskUsage, diskUsageStat{
				Mountpoint:  p.Mountpoint,
				Total:       usage.Total,
				Used:        usage.Used,
				Free:        usage.Free,
				UsedPercent: usage.UsedPercent,
			})
		}

		return hostMsg{
			cpuUsage:    cpuUsage,
			memoryUsage: memUsage,
			diskUsage:   diskUsage,
		}
	}
}

func getLogs(clientset *kubernetes.Clientset, namespace, podName string) tea.Cmd {
	return func() tea.Msg {
		podLogOpts := v1.PodLogOptions{}
		req := clientset.CoreV1().Pods(namespace).GetLogs(podName, &podLogOpts)
		podLogs, err := req.Stream(context.Background())
		if err != nil {
			return errMsg{err}
		}
		defer podLogs.Close()

		var buf bytes.Buffer
		_, err = io.Copy(&buf, podLogs)
		if err != nil {
			return errMsg{err}
		}
		return logsMsg{logs: buf.String()}
	}
}

func getHostLogs(logType string) tea.Cmd {
	return func() tea.Msg {
		var args []string
		cmd := ""
		switch logType {
		case "System Logs":
			cmd = "journalctl"
		case "Kubelet Logs":
			cmd = "journalctl"
			args = []string{"-u", "kubelet.service"}
		case "Docker Logs":
			cmd = "journalctl"
			args = []string{"-u", "docker.service"}
		case "dmesg":
			cmd = "dmesg"
		default:
			return errMsg{fmt.Errorf("unknown log type: %s", logType)}
		}

		c := exec.Command(cmd, args...)
		var out bytes.Buffer
		c.Stdout = &out
		err := c.Run()
		if err != nil {
			return errMsg{err}
		}
		return hostLogsMsg{logs: out.String()}
	}
}

func getContainers() tea.Cmd {
	return func() tea.Msg {
		cmd := "docker ps --format '{{.Names}}'"
		c := exec.Command("bash", "-c", cmd)
		var out bytes.Buffer
		c.Stdout = &out
		err := c.Run()
		if err != nil {
			return errMsg{err}
		}
		containers := strings.Split(strings.TrimSpace(out.String()), "\n")
		return appLogsMsg{containers: containers}
	}
}

func getContainerLogs(containerName string) tea.Cmd {
	return func() tea.Msg {
		c := exec.Command("docker", "logs", containerName)
		var out bytes.Buffer
		c.Stdout = &out
		err := c.Run()
		if err != nil {
			return errMsg{err}
		}
		return containerLogsMsg{logs: out.String()}
	}
}
func getNodes(clientset *kubernetes.Clientset, metricsClientset *metrics.Clientset) tea.Cmd {
	return func() tea.Msg {
		nodes, err := clientset.CoreV1().Nodes().List(context.Background(), metav1.ListOptions{})
		if err != nil {
			return errMsg{err}
		}
		metricsMap := make(map[string]v1beta1.NodeMetrics)
		metricsList, err := metricsClientset.MetricsV1beta1().NodeMetricses().List(context.Background(), metav1.ListOptions{})
		if err == nil {
			for _, m := range metricsList.Items {
				metricsMap[m.Name] = m
			}
		}
		return nodesMsg{nodes: nodes.Items, metrics: metricsMap}
	}
}

func getPods(clientset *kubernetes.Clientset, metricsClientset *metrics.Clientset, namespace string) tea.Cmd {
	return func() tea.Msg {
		pods, err := clientset.CoreV1().Pods(namespace).List(context.Background(), metav1.ListOptions{})
		if err != nil {
			return errMsg{err}
		}
		metricsMap := make(map[string]v1beta1.PodMetrics)
		metricsList, err := metricsClientset.MetricsV1beta1().PodMetricses(namespace).List(context.Background(), metav1.ListOptions{})
		if err == nil {
			for _, m := range metricsList.Items {
				metricsMap[m.Name] = m
			}
		}
		return podsMsg{pods: pods.Items, metrics: metricsMap}
	}
}

func getPVCs(clientset *kubernetes.Clientset, namespace string) tea.Cmd {
	return func() tea.Msg {
		pvcs, err := clientset.CoreV1().PersistentVolumeClaims(namespace).List(context.Background(), metav1.ListOptions{})
		if err != nil {
			return errMsg{err}
		}
		return pvcsMsg{pvcs.Items}
	}
}

func getPVs(clientset *kubernetes.Clientset) tea.Cmd {
	return func() tea.Msg {
		pvs, err := clientset.CoreV1().PersistentVolumes().List(context.Background(), metav1.ListOptions{})
		if err != nil {
			return errMsg{err}
		}
		return pvsMsg{pvs.Items}
	}
}

func getDeployments(clientset *kubernetes.Clientset, namespace string) tea.Cmd {
	return func() tea.Msg {
		deployments, err := clientset.AppsV1().Deployments(namespace).List(context.Background(), metav1.ListOptions{})
		if err != nil {
			return errMsg{err}
		}
		return deploymentsMsg{deployments.Items}
	}
}

func getStatefulSets(clientset *kubernetes.Clientset, namespace string) tea.Cmd {
	return func() tea.Msg {
		statefulsets, err := clientset.AppsV1().StatefulSets(namespace).List(context.Background(), metav1.ListOptions{})
		if err != nil {
			return errMsg{err}
		}
		return statefulsetsMsg{statefulsets.Items}
	}
}

func getDaemonSets(clientset *kubernetes.Clientset, namespace string) tea.Cmd {
	return func() tea.Msg {
		daemonsets, err := clientset.AppsV1().DaemonSets(namespace).List(context.Background(), metav1.ListOptions{})
		if err != nil {
			return errMsg{err}
		}
		return daemonsetsMsg{daemonsets.Items}
	}
}

func getServices(clientset *kubernetes.Clientset, namespace string) tea.Cmd {
	return func() tea.Msg {
		services, err := clientset.CoreV1().Services(namespace).List(context.Background(), metav1.ListOptions{})
		if err != nil {
			return errMsg{err}
		}
		return servicesMsg{services.Items}
	}
}

func getNetworkPolicies(clientset *kubernetes.Clientset, namespace string) tea.Cmd {
	return func() tea.Msg {
		policies, err := clientset.NetworkingV1().NetworkPolicies(namespace).List(context.Background(), metav1.ListOptions{})
		if err != nil {
			return errMsg{err}
		}
		return networkPoliciesMsg{policies.Items}
	}
}

func getEvents(clientset *kubernetes.Clientset, namespace string) tea.Cmd {
	return func() tea.Msg {
		events, err := clientset.CoreV1().Events(namespace).List(context.Background(), metav1.ListOptions{})
		if err != nil {
			return errMsg{err}
		}
		sort.Slice(events.Items, func(i, j int) bool {
			return events.Items[i].LastTimestamp.Time.After(events.Items[j].LastTimestamp.Time)
		})
		return eventsMsg{events.Items}
	}
}

func getNamespaces(clientset *kubernetes.Clientset) tea.Cmd {
	return func() tea.Msg {
		ns, err := clientset.CoreV1().Namespaces().List(context.Background(), metav1.ListOptions{})
		if err != nil {
			return errMsg{err}
		}
		return namespacesMsg{ns.Items}
	}
}

// getResourceYAML fetches a resource and returns its YAML representation.
func getResourceYAML(clientset *kubernetes.Clientset, namespace, name, kind string) tea.Cmd {
	return func() tea.Msg {
		var obj runtime.Object
		var err error

		switch kind {
		case "Pod":
			obj, err = clientset.CoreV1().Pods(namespace).Get(context.Background(), name, metav1.GetOptions{})
		case "Deployment":
			obj, err = clientset.AppsV1().Deployments(namespace).Get(context.Background(), name, metav1.GetOptions{})
		case "StatefulSet":
			obj, err = clientset.AppsV1().StatefulSets(namespace).Get(context.Background(), name, metav1.GetOptions{})
		case "DaemonSet":
			obj, err = clientset.AppsV1().DaemonSets(namespace).Get(context.Background(), name, metav1.GetOptions{})
		case "Service":
			obj, err = clientset.CoreV1().Services(namespace).Get(context.Background(), name, metav1.GetOptions{})
		case "PersistentVolumeClaim":
			obj, err = clientset.CoreV1().PersistentVolumeClaims(namespace).Get(context.Background(), name, metav1.GetOptions{})
		case "PersistentVolume":
			obj, err = clientset.CoreV1().PersistentVolumes().Get(context.Background(), name, metav1.GetOptions{})
		case "NetworkPolicy":
			obj, err = clientset.NetworkingV1().NetworkPolicies(namespace).Get(context.Background(), name, metav1.GetOptions{})
		case "Node":
			obj, err = clientset.CoreV1().Nodes().Get(context.Background(), name, metav1.GetOptions{})
		case "Event":
			obj, err = clientset.CoreV1().Events(namespace).Get(context.Background(), name, metav1.GetOptions{})
		case "Namespace":
			obj, err = clientset.CoreV1().Namespaces().Get(context.Background(), name, metav1.GetOptions{})
		default:
			return errMsg{fmt.Errorf("unsupported resource kind for YAML: %s", kind)}
		}

		if err != nil {
			return errMsg{err}
		}

		// Convert to YAML
		s := json.NewYAMLSerializer(json.DefaultMetaFactory, scheme.Scheme, scheme.Scheme)
		var b bytes.Buffer
		if err := s.Encode(obj, &b); err != nil {
			return errMsg{err}
		}

		return yamlMsg{yaml: b.String()}
	}
}

// getDashboardMetrics fetches and aggregates cluster-wide resource utilization metrics.
func getDashboardMetrics(clientset *kubernetes.Clientset, metricsClientset *metrics.Clientset, styles Styles) tea.Cmd {
	return func() tea.Msg {
		var totalCPUCapacity, totalMemoryCapacity resource.Quantity
		var totalCPUUsage, totalMemoryUsage resource.Quantity

		// Get Nodes and Node Metrics
		nodes, err := clientset.CoreV1().Nodes().List(context.Background(), metav1.ListOptions{})
		if err != nil {
			return errMsg{err}
		}
		nodeMetricsList, err := metricsClientset.MetricsV1beta1().NodeMetricses().List(context.Background(), metav1.ListOptions{})
		if err != nil {
			return errMsg{err}
		}
		nodeMetricsMap := make(map[string]v1beta1.NodeMetrics)
		for _, nm := range nodeMetricsList.Items {
			nodeMetricsMap[nm.Name] = nm
		}

		// Aggregate Node Capacity and Usage
		for _, node := range nodes.Items {
			totalCPUCapacity.Add(*node.Status.Capacity.Cpu())
			totalMemoryCapacity.Add(*node.Status.Capacity.Memory())
			if nm, ok := nodeMetricsMap[node.Name]; ok {
				totalCPUUsage.Add(*nm.Usage.Cpu())
				totalMemoryUsage.Add(*nm.Usage.Memory())
			}
		}

		// Get Pods and Pod Metrics
		pods, err := clientset.CoreV1().Pods("").List(context.Background(), metav1.ListOptions{}) // All namespaces
		if err != nil {
			return errMsg{err}
		}
		podMetricsList, err := metricsClientset.MetricsV1beta1().PodMetricses("").List(context.Background(), metav1.ListOptions{}) // All namespaces
		if err != nil {
			return errMsg{err}
		}
		podMetricsMap := make(map[string]v1beta1.PodMetrics)
		for _, pm := range podMetricsList.Items {
			podMetricsMap[pm.Name] = pm
		}

		// Prepare for sorting top pods/nodes
		type podWithMetrics struct {
			v1.Pod
			CPUUsage    *resource.Quantity
			MemoryUsage *resource.Quantity
		}
		type nodeWithMetrics struct {
			v1.Node
			CPUUsage    *resource.Quantity
			MemoryUsage *resource.Quantity
		}

		var podsWithMetrics []podWithMetrics
		for _, pod := range pods.Items {
			if pm, ok := podMetricsMap[pod.Name]; ok {
				podsWithMetrics = append(podsWithMetrics, podWithMetrics{
					Pod:         pod,
					CPUUsage:    totalPodCPU(pm),
					MemoryUsage: totalPodMemory(pm),
				})
			}
		}

		var nodesWithMetrics []nodeWithMetrics
		for _, node := range nodes.Items {
			if nm, ok := nodeMetricsMap[node.Name]; ok {
				nodesWithMetrics = append(nodesWithMetrics, nodeWithMetrics{
					Node:        node,
					CPUUsage:    nm.Usage.Cpu(),
					MemoryUsage: nm.Usage.Memory(),
				})
			}
		}

		// Sort pods by CPU usage
		podsByCPU := make([]podWithMetrics, len(podsWithMetrics))
		copy(podsByCPU, podsWithMetrics)
		sort.Slice(podsByCPU, func(i, j int) bool {
			return podsByCPU[i].CPUUsage.Cmp(*podsByCPU[j].CPUUsage) > 0
		})

		// Sort pods by Memory usage
		podsByMemory := make([]podWithMetrics, len(podsWithMetrics))
		copy(podsByMemory, podsWithMetrics)
		sort.Slice(podsByMemory, func(i, j int) bool {
			return podsByMemory[i].MemoryUsage.Cmp(*podsByMemory[j].MemoryUsage) > 0
		})

		// Sort nodes by CPU usage
		nodesByCPU := make([]nodeWithMetrics, len(nodesWithMetrics))
		copy(nodesByCPU, nodesWithMetrics)
		sort.Slice(nodesByCPU, func(i, j int) bool {
			return nodesByCPU[i].CPUUsage.Cmp(*nodesByCPU[j].CPUUsage) > 0
		})

		// Sort nodes by Memory usage
		nodesByMemory := make([]nodeWithMetrics, len(nodesWithMetrics))
		copy(nodesByMemory, nodesWithMetrics)
		sort.Slice(nodesByMemory, func(i, j int) bool {
			return nodesByMemory[i].MemoryUsage.Cmp(*nodesByMemory[j].MemoryUsage) > 0
		})

		// Get top N (e.g., 5)
		topN := 5
		var topPodsCPU, topPodsMem []v1.Pod
		for i := 0; i < len(podsByCPU) && i < topN; i++ {
			topPodsCPU = append(topPodsCPU, podsByCPU[i].Pod)
		}
		for i := 0; i < len(podsByMemory) && i < topN; i++ {
			topPodsMem = append(topPodsMem, podsByMemory[i].Pod)
		}

		var topNodesCPU, topNodesMem []v1.Node
		for i := 0; i < len(nodesByCPU) && i < topN; i++ {
			topNodesCPU = append(topNodesCPU, nodesByCPU[i].Node)
		}
		for i := 0; i < len(nodesByMemory) && i < topN; i++ {
			topNodesMem = append(topNodesMem, nodesByMemory[i].Node)
		}

		var podCPUData []barchart.BarData
		for _, p := range topPodsCPU {
			podCPUData = append(podCPUData, barchart.BarData{Label: p.Name, Values: []barchart.BarValue{{Value: float64(totalPodCPU(podMetricsMap[p.Name]).MilliValue()), Style: styles.ChartBar}}})
		}

		var podMemoryData []barchart.BarData
		for _, p := range topPodsMem {
			podMemoryData = append(podMemoryData, barchart.BarData{Label: p.Name, Values: []barchart.BarValue{{Value: float64(totalPodMemory(podMetricsMap[p.Name]).Value() / (1024 * 1024)), Style: styles.ChartBar}}})
		}

		var nodeCPUData []barchart.BarData
		for _, n := range topNodesCPU {
			cpu := nodeMetricsMap[n.Name].Usage[v1.ResourceCPU]
			nodeCPUData = append(nodeCPUData, barchart.BarData{Label: n.Name, Values: []barchart.BarValue{{Value: float64((&cpu).MilliValue()), Style: styles.ChartBar}}})
		}

		var nodeMemoryData []barchart.BarData
		for _, n := range topNodesMem {
			mem := nodeMetricsMap[n.Name].Usage[v1.ResourceMemory]
			nodeMemoryData = append(nodeMemoryData, barchart.BarData{Label: n.Name, Values: []barchart.BarValue{{Value: float64((&mem).Value() / (1024 * 1024)), Style: styles.ChartBar}}})
		}

		return dashboardMsg{
			clusterCPUUsage:    fmt.Sprintf("%s / %s (%s%%)", formatMilliCPU(&totalCPUUsage), formatMilliCPU(&totalCPUCapacity), formatPercentage(totalCPUUsage.MilliValue(), totalCPUCapacity.MilliValue())),
			clusterMemoryUsage: fmt.Sprintf("%s / %s (%s%%)", formatMiBMemory(&totalMemoryUsage), formatMiBMemory(&totalMemoryCapacity), formatPercentage(totalMemoryUsage.Value(), totalMemoryCapacity.Value())),
			topPodsByCPU:       topPodsCPU,
			topPodsByMemory:    topPodsMem,
			topNodesByCPU:      topNodesCPU,
			topNodesByMemory:   topNodesMem,
			podCPUChartData:    podCPUData,
			podMemoryChartData: podMemoryData,
			nodeCPUChartData:   nodeCPUData,
			nodeMemoryChartData: nodeMemoryData,
		}
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(
		doTick(),
		getDashboardMetrics(m.clientset, m.metricsClientset, m.styles), // Fetch dashboard metrics on init
		getHostMetrics(), // Fetch host metrics on init
	)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.viewport.Width = msg.Width
		m.viewport.Height = msg.Height - 10 // Adjust for header/footer
		chartWidth := msg.Width / 2
		chartHeight := msg.Height / 3
		m.podCPUChart = barchart.New(chartWidth, chartHeight)
		m.podMemoryChart = barchart.New(chartWidth, chartHeight)
		m.nodeCPUChart = barchart.New(chartWidth, chartHeight)
		m.nodeMemoryChart = barchart.New(chartWidth, chartHeight)
		m.hostCPUChart = barchart.New(chartWidth, chartHeight)
		m.hostMemoryChart = barchart.New(chartWidth, chartHeight)
		m.ready = true
		return m, nil
	case tickMsg:
		switch m.view {
		case viewNodes:
			return m, getNodes(m.clientset, m.metricsClientset)
		case viewPods:
			return m, getPods(m.clientset, m.metricsClientset, m.selectedNamespace)
		case viewPVCs:
			return m, getPVCs(m.clientset, m.selectedNamespace)
		case viewPVs:
			return m, getPVs(m.clientset)
		case viewDeployments:
			return m, getDeployments(m.clientset, m.selectedNamespace)
		case viewStatefulSets:
			return m, getStatefulSets(m.clientset, m.selectedNamespace)
		case viewDaemonSets:
			return m, getDaemonSets(m.clientset, m.selectedNamespace)
		case viewServices:
			return m, getServices(m.clientset, m.selectedNamespace)
		case viewNetworkPolicies:
			return m, getNetworkPolicies(m.clientset, m.selectedNamespace)
		case viewEvents:
			return m, getEvents(m.clientset, m.selectedNamespace)
		case viewNamespaces:
			return m, getNamespaces(m.clientset)
		case viewDashboard:
			return m, getDashboardMetrics(m.clientset, m.metricsClientset, m.styles)
		case viewHostDashboard:
			return m, getHostMetrics()

		}
		return m, doTick()
	case nodesMsg:
		m.nodes = msg.nodes
		m.nodeMetrics = msg.metrics
		return m, nil
	case podsMsg:
		m.pods = msg.pods
		m.podMetrics = msg.metrics
		return m, nil
	case pvcsMsg:
		m.pvcs = msg.pvcs
		return m, nil
	case pvsMsg:
		m.pvs = msg.pvs
		return m, nil
	case deploymentsMsg:
		m.deployments = msg.deployments
		return m, nil
	case statefulsetsMsg:
		m.statefulsets = msg.statefulsets
		return m, nil
	case daemonsetsMsg:
		m.daemonsets = msg.daemonsets
		return m, nil
	case servicesMsg:
		m.services = msg.services
		return m, nil
	case networkPoliciesMsg:
		m.netpols = msg.policies
		return m, nil
	case eventsMsg:
		m.events = msg.events
		return m, nil
	case namespacesMsg:
		m.namespaces = msg.namespaces
		return m, nil
	case logsMsg:
		m.details = msg.logs
		m.viewport.SetContent(m.details)
		m.viewport.GotoTop() // Scroll to top
		return m, nil
	case hostMsg:
		cpuBarData := barchart.BarData{
			Values: []barchart.BarValue{{Value: msg.cpuUsage, Style: m.styles.ChartBar}},
		}
		memBarData := barchart.BarData{
			Values: []barchart.BarValue{{Value: msg.memoryUsage, Style: m.styles.ChartBar}},
		}
		m.hostCPUChart.Push(cpuBarData)
		m.hostMemoryChart.Push(memBarData)
		m.hostDiskUsage = msg.diskUsage
		return m, nil
	case appLogsMsg:
		m.containers = msg.containers
		return m, nil
	case hostLogsMsg:
		m.details = msg.logs
		m.viewport.SetContent(m.details)
		m.viewport.GotoTop()
		return m, nil

	case containerLogsMsg:
		m.details = msg.logs
		m.viewport.SetContent(m.details)
		m.viewport.GotoTop()
		return m, nil
	case yamlMsg:
		m.yamlContent = msg.yaml
		m.viewport.SetContent(m.yamlContent)
		m.viewport.GotoTop()
		return m, nil
	case dashboardMsg:
		m.clusterCPUUsage = msg.clusterCPUUsage
		m.clusterMemoryUsage = msg.clusterMemoryUsage
		m.topPodsByCPU = msg.topPodsByCPU
		m.topPodsByMemory = msg.topPodsByMemory
		m.topNodesByCPU = msg.topNodesByCPU
		m.topNodesByMemory = msg.topNodesByMemory
		m.podCPUChart.PushAll(msg.podCPUChartData)
		m.podMemoryChart.PushAll(msg.podMemoryChartData)
		m.nodeCPUChart.PushAll(msg.nodeCPUChartData)
		m.nodeMemoryChart.PushAll(msg.nodeMemoryChartData)
		return m, nil
	case scaleMsg:
		m.setView(m.previousView) // Go back to the previous view
		// Trigger a refresh of the view
		switch m.view {
		case viewDeployments:
			return m, getDeployments(m.clientset, m.selectedNamespace)
		}
		return m, nil
	case podDeletedMsg:
		m.setView(viewPods) // Go back to the previous view
		return m, getPods(m.clientset, m.metricsClientset, m.selectedNamespace)
	case errMsg:
		m.err = msg.err
		return m, nil
	case tea.KeyMsg:
		if m.view == viewScaling {
			switch msg.String() {
			case "enter":
				replicas, err := strconv.Atoi(m.textInput.Value())
				if err == nil {
					switch m.previousView {
					case viewDeployments:
						deployment := m.deployments[m.cursor]
						return m, scaleDeployment(m.clientset, deployment.Namespace, deployment.Name, int32(replicas))
					}
				}
			case "q", "esc":
				m.setView(m.previousView)
				return m, nil
			}
			m.textInput, cmd = m.textInput.Update(msg)
			return m, cmd
		}
		if m.view == viewConfirmDelete {
			switch msg.String() {
			case "y", "Y":
				switch m.previousView {
				case viewPods:
					pod := m.pods[m.cursor]
					return m, deletePod(m.clientset, pod.Namespace, pod.Name)
				}
			case "n", "N", "q", "esc":
				m.setView(m.previousView)
				return m, nil
			}
			return m, nil
		}

		switch msg.String() {
		case "q", "ctrl+c":
			if m.view != viewResourceMenu {
				m.setView(viewResourceMenu)
				return m, nil
			}
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			switch m.view {
			case viewResourceMenu:
				if m.cursor < len(m.resourceTypes)-1 {
					m.cursor++
				}
			case viewNodes:
				if m.cursor < len(m.nodes)-1 {
					m.cursor++
				}
			case viewPods:
				if m.cursor < len(m.pods)-1 {
					m.cursor++
				}
			case viewPVCs:
				if m.cursor < len(m.pvcs)-1 {
					m.cursor++
				}
			case viewPVs:
				if m.cursor < len(m.pvs)-1 {
					m.cursor++
				}
			case viewDeployments:
				if m.cursor < len(m.deployments)-1 {
					m.cursor++
				}
			case viewStatefulSets:
				if m.cursor < len(m.statefulsets)-1 {
					m.cursor++
				}
			case viewDaemonSets:
				if m.cursor < len(m.daemonsets)-1 {
					m.cursor++
				}
			case viewServices:
				if m.cursor < len(m.services)-1 {
					m.cursor++
				}
			case viewNetworkPolicies:
				if m.cursor < len(m.netpols)-1 {
					m.cursor++
				}
			case viewEvents:
				if m.cursor < len(m.events)-1 {
					m.cursor++
				}
			case viewNamespaces:
				if m.cursor < len(m.namespaces)-1 {
					m.cursor++
				}
			case viewHostDashboard:
				switch m.hostTabs[m.activeHostTab] {
				case "System Logs":
					if m.cursor < len(m.hostLogTypes)-1 {
						m.cursor++
					}
				case "Application Logs":
					if m.cursor < len(m.containers)-1 {
						m.cursor++
					}
				}
			}
		case "right", "l":
			if m.view == viewHostDashboard {
				m.activeHostTab = (m.activeHostTab + 1) % len(m.hostTabs)
				m.details = ""
				m.viewport.SetContent("")
				m.cursor = 0
				if m.hostTabs[m.activeHostTab] == "Application Logs" {
					return m, getContainers()
				}
			}
		case "left", "h":
			if m.view == viewHostDashboard {
				m.activeHostTab = (m.activeHostTab - 1 + len(m.hostTabs)) % len(m.hostTabs)
			}
		case "d": // Details or Describe
			switch m.view {
			case viewNodes:
				m.details = formatNodeDetails(m.nodes[m.cursor])
			case viewPods:
				m.details = formatPodDetails(m.pods[m.cursor])
			case viewPVCs:
				m.details = formatPVCDetails(m.pvcs[m.cursor])
			case viewPVs:
				m.details = formatPVDetails(m.pvs[m.cursor])
			case viewDeployments:
				m.details = formatDeploymentDetails(m.deployments[m.cursor])
			case viewStatefulSets:
				m.details = formatStatefulSetDetails(m.statefulsets[m.cursor])
			case viewDaemonSets:
				m.details = formatDaemonSetDetails(m.daemonsets[m.cursor])
			case viewServices:
				m.details = formatServiceDetails(m.services[m.cursor])
			case viewNetworkPolicies:
				m.details = formatNetworkPolicyDetails(m.netpols[m.cursor])
			case viewEvents:
				m.details = formatEventDetails(m.events[m.cursor])
			}
			m.setView(viewDetails)
		case "y": // View YAML
			var namespace, name, kind string
			viewToCheck := m.view
			if m.view == viewDetails {
				viewToCheck = m.previousView
			}
			switch viewToCheck {
			case viewNodes:
				node := m.nodes[m.cursor]
				namespace, name, kind = "", node.Name, "Node"
			case viewPods:
				pod := m.pods[m.cursor]
				namespace, name, kind = pod.Namespace, pod.Name, "Pod"
			case viewPVCs:
				pvc := m.pvcs[m.cursor]
				namespace, name, kind = pvc.Namespace, pvc.Name, "PersistentVolumeClaim"
			case viewPVs:
				pv := m.pvs[m.cursor]
				namespace, name, kind = "", pv.Name, "PersistentVolume"
			case viewDeployments:
				deployment := m.deployments[m.cursor]
				namespace, name, kind = deployment.Namespace, deployment.Name, "Deployment"
			case viewStatefulSets:
				statefulset := m.statefulsets[m.cursor]
				namespace, name, kind = statefulset.Namespace, statefulset.Name, "StatefulSet"
			case viewDaemonSets:
				daemonset := m.daemonsets[m.cursor]
				namespace, name, kind = daemonset.Namespace, daemonset.Name, "DaemonSet"
			case viewServices:
				service := m.services[m.cursor]
				namespace, name, kind = service.Namespace, service.Name, "Service"
			case viewNetworkPolicies:
				policy := m.netpols[m.cursor]
				namespace, name, kind = policy.Namespace, policy.Name, "NetworkPolicy"
			case viewNamespaces:
				ns := m.namespaces[m.cursor]
				namespace, name, kind = "", ns.Name, "Namespace"
			}
			if kind != "" {
				m.setView(viewYAML)
				return m, getResourceYAML(m.clientset, namespace, name, kind)
			}
		case "L": // View Logs
			if m.view == viewPods || (m.view == viewDetails && m.previousView == viewPods) {
				pod := m.pods[m.cursor]
				m.setView(viewLogs)
				return m, getLogs(m.clientset, pod.Namespace, pod.Name)
			}
		case "S": // Scale
			if m.view == viewDeployments || (m.view == viewDetails && m.previousView == viewDeployments) {
				m.textInput.SetValue("")
				m.textInput.Focus()
				m.setView(viewScaling)
			}
		case "X": // Delete
			if m.view == viewPods || (m.view == viewDetails && m.previousView == viewPods) {
				m.setView(viewConfirmDelete)
			}
		case "esc":
			m.setView(m.previousView)
		case "enter":
			switch m.view {
			case viewNodes:
				if len(m.nodes) > m.cursor {
					m.details = formatNodeDetails(m.nodes[m.cursor])
					m.setView(viewDetails)
				}
			case viewPods:
				if len(m.pods) > m.cursor {
					m.details = formatPodDetails(m.pods[m.cursor])
					m.setView(viewDetails)
				}
			case viewPVCs:
				if len(m.pvcs) > m.cursor {
					m.details = formatPVCDetails(m.pvcs[m.cursor])
					m.setView(viewDetails)
				}
			case viewPVs:
				if len(m.pvs) > m.cursor {
					m.details = formatPVDetails(m.pvs[m.cursor])
					m.setView(viewDetails)
				}
			case viewDeployments:
				if len(m.deployments) > m.cursor {
					m.details = formatDeploymentDetails(m.deployments[m.cursor])
					m.setView(viewDetails)
				}
			case viewStatefulSets:
				if len(m.statefulsets) > m.cursor {
					m.details = formatStatefulSetDetails(m.statefulsets[m.cursor])
					m.setView(viewDetails)
				}
			case viewDaemonSets:
				if len(m.daemonsets) > m.cursor {
					m.details = formatDaemonSetDetails(m.daemonsets[m.cursor])
					m.setView(viewDetails)
				}
			case viewServices:
				if len(m.services) > m.cursor {
					m.details = formatServiceDetails(m.services[m.cursor])
					m.setView(viewDetails)
				}
			case viewNetworkPolicies:
				if len(m.netpols) > m.cursor {
					m.details = formatNetworkPolicyDetails(m.netpols[m.cursor])
					m.setView(viewDetails)
				}
			case viewEvents:
				if len(m.events) > m.cursor {
					m.details = formatEventDetails(m.events[m.cursor])
					m.setView(viewDetails)
				}
			case viewResourceMenu:
				selected := m.resourceTypes[m.cursor]
				switch selected {
				case "Cluster Dashboard":
					m.setView(viewDashboard)
					return m, getDashboardMetrics(m.clientset, m.metricsClientset, m.styles)
				case "Host Dashboard":
					m.setView(viewHostDashboard)
					return m, getHostMetrics()
				case "Nodes":
					m.setView(viewNodes)
					return m, getNodes(m.clientset, m.metricsClientset)
				case "Pods":
					m.setView(viewPods)
					return m, getPods(m.clientset, m.metricsClientset, m.selectedNamespace)
				case "PersistentVolumeClaims":
					m.setView(viewPVCs)
					return m, getPVCs(m.clientset, m.selectedNamespace)
				case "PersistentVolumes":
					m.setView(viewPVs)
					return m, getPVs(m.clientset)
				case "Deployments":
					m.setView(viewDeployments)
					return m, getDeployments(m.clientset, m.selectedNamespace)
				case "StatefulSets":
					m.setView(viewStatefulSets)
					return m, getStatefulSets(m.clientset, m.selectedNamespace)
				case "DaemonSets":
					m.setView(viewDaemonSets)
					return m, getDaemonSets(m.clientset, m.selectedNamespace)
				case "Services":
					m.setView(viewServices)
					return m, getServices(m.clientset, m.selectedNamespace)
				case "NetworkPolicies":
					m.setView(viewNetworkPolicies)
					return m, getNetworkPolicies(m.clientset, m.selectedNamespace)
				case "Events":
					m.setView(viewEvents)
					return m, getEvents(m.clientset, m.selectedNamespace)
				case "Namespaces":
					m.setView(viewNamespaces)
					return m, getNamespaces(m.clientset)
				}
			case viewNamespaces:
				if m.cursor == 0 { // "all"
					m.selectedNamespace = ""
				} else {
					m.selectedNamespace = m.namespaces[m.cursor-1].Name
				}
				m.setView(viewResourceMenu)
			case viewHostDashboard:
				switch m.hostTabs[m.activeHostTab] {
				case "System Logs":
					selectedLogType := m.hostLogTypes[m.cursor]
					return m, getHostLogs(selectedLogType)
				case "Application Logs":
					if len(m.containers) > 0 {
						selectedContainer := m.containers[m.cursor]
						return m, getContainerLogs(selectedContainer)
					}
				}
			}

		case "H": // Go to Host Dashboard
			m.setView(viewHostDashboard)
			return m, getHostMetrics()
		case "D": // Go to Cluster Dashboard
			m.setView(viewDashboard)
			return m, getDashboardMetrics(m.clientset, m.metricsClientset, m.styles)
		}
	}
	if m.view == viewDetails || m.view == viewLogs || m.view == viewYAML {
		m.viewport, cmd = m.viewport.Update(msg)
		cmds = append(cmds, cmd)
	}
	return m, tea.Batch(cmds...)
}

func (m model) View() string {
	if m.err != nil {
		return fmt.Sprintf("Error: %v", m.err)
	}
	if !m.ready {
		return "Initializing..."
	}

	s := strings.Builder{}
	s.WriteString(renderHeader(m))

	switch m.view {
	case viewHelp:
		s.WriteString(renderHelp())
	case viewResourceMenu:
		s.WriteString(renderResourceMenu(m))
	case viewNodes:
		s.WriteString(renderNodes(m))
	case viewPods:
		s.WriteString(renderPods(m))
	case viewPVCs:
		s.WriteString(renderPVCs(m))
	case viewPVs:
		s.WriteString(renderPVs(m))
	case viewDeployments:
		s.WriteString(renderDeployments(m))
	case viewStatefulSets:
		s.WriteString(renderStatefulSets(m))
	case viewDaemonSets:
		s.WriteString(renderDaemonSets(m))
	case viewServices:
		s.WriteString(renderServices(m))
	case viewNetworkPolicies:
		s.WriteString(renderNetworkPolicies(m))
	case viewEvents:
		s.WriteString(renderEvents(m))
	case viewNamespaces:
		s.WriteString(renderNamespaces(m))
	case viewDetails, viewLogs, viewYAML:
		s.WriteString(m.viewport.View())
		switch m.previousView {
		case viewPods:
			s.WriteString("\n\n(L)ogs | (X)delete | (Y)AML")
		case viewDeployments:
			s.WriteString("\n\n(S)cale Replicas")
		}
	case viewScaling:
		s.WriteString("Scale Deployment:\n")
		s.WriteString(m.textInput.View())
	case viewConfirmDelete:
		s.WriteString(fmt.Sprintf("Are you sure you want to delete pod %s? (y/n)", m.pods[m.cursor].Name))
	case viewDashboard:
		s.WriteString(renderDashboard(m))
	case viewHostDashboard:
		s.WriteString(renderHostDashboard(m))
	}

	s.WriteString(renderFooter(m))
	return s.String()
}

func renderHeader(m model) string {
	ns := m.selectedNamespace
	if ns == "" {
		ns = "all"
	}
	header := fmt.Sprintf("kubeview | Namespace: %s | Press '?' for help", ns)
	return m.styles.Header.Render(header)
}

func renderFooter(m model) string {
	var help string
	switch m.view {
	case viewHostDashboard:
		help = " (l/h) change tab | (↑/↓) navigate"
	case viewDetails:
		switch m.previousView {
		case viewPods:
			help = " (L)ogs | (X)delete | (Y)AML"
		case viewDeployments:
			help = " (S)cale Replicas"
		}
	default:
		help = " (q)uit | (b)ack to menu | (↑/↓) navigate"
	}

	return m.styles.Footer.Render(help)
}
func renderHelp() string {
	return `
 kubeview Help:

 (q) or (ctrl+c) - Quit
 (esc) - Go back to the previous view
 (H) - Go to Host Dashboard
 (D) - Go to Cluster Dashboard
 (↑/k) - Move cursor up
 (↓/j) - Move cursor down
 (enter) - Select / View details
 (y) - View YAML for the selected resource
 (L) - View logs for the selected pod
 (S) - Scale the selected deployment
 (X) - Delete the selected pod
`
}

func renderResourceMenu(m model) string {
	s := "Select a resource type:\n\n"
	for i, rt := range m.resourceTypes {
		if i == m.cursor {
			s += m.styles.SelectedItem.Render("> " + rt)
		} else {
			s += "  " + rt
		}
		s += "\n"
	}
	return s
}

func renderNodes(m model) string {
	s := "Nodes:\n\n"
	header := fmt.Sprintf("%-40s %-15s %-15s %-15s %-15s", "NAME", "STATUS", "VERSION", "CPU (m)", "MEM (Mi)")
	s += m.styles.TableHeader.Render(header) + "\n"
	for i, node := range m.nodes {
		status := "Unknown"
		for _, cond := range node.Status.Conditions {
			if cond.Type == v1.NodeReady {
				status = string(cond.Status)
				break
			}
		}

		cpuUsage, memUsage := "N/A", "N/A"
		if metrics, ok := m.nodeMetrics[node.Name]; ok {
			cpu := metrics.Usage[v1.ResourceCPU]
			mem := metrics.Usage[v1.ResourceMemory]
			cpuUsage = formatMilliCPU(&cpu)
			memUsage = formatMiBMemory(&mem)
		}

		line := fmt.Sprintf("%-40s %-15s %-15s %-15s %-15s",
			node.Name,
			status,
			node.Status.NodeInfo.KubeletVersion,
			cpuUsage,
			memUsage,
		)

		if i == m.cursor {
			s += m.styles.SelectedItem.Render("> " + line)
		} else {
			s += "  " + line
		}
		s += "\n"
	}
	return s
}

func renderPods(m model) string {
	s := "Pods:\n\n"
	header := fmt.Sprintf("%-50s %-20s %-15s %-15s %-15s", "NAME", "STATUS", "RESTARTS", "CPU (m)", "MEM (Mi)")
	s += m.styles.TableHeader.Render(header) + "\n"
	for i, pod := range m.pods {
		restarts := 0
		for _, cs := range pod.Status.ContainerStatuses {
			restarts += int(cs.RestartCount)
		}

		cpuUsage, memUsage := "N/A", "N/A"
		if metrics, ok := m.podMetrics[pod.Name]; ok {
			cpuUsage = formatMilliCPU(totalPodCPU(metrics))
			memUsage = formatMiBMemory(totalPodMemory(metrics))
		}

		line := fmt.Sprintf("%-50s %-20s %-15d %-15s %-15s",
			pod.Name,
			pod.Status.Phase,
			restarts,
			cpuUsage,
			memUsage,
		)

		if i == m.cursor {
			s += m.styles.SelectedItem.Render("> " + line)
		} else {
			s += "  " + line
		}
		s += "\n"
	}
	return s
}

func renderPVCs(m model) string {
	s := "PersistentVolumeClaims:\n\n"
	header := fmt.Sprintf("%-50s %-20s %-15s %-15s", "NAME", "STATUS", "VOLUME", "CAPACITY")
	s += m.styles.TableHeader.Render(header) + "\n"
	for i, pvc := range m.pvcs {
		capacity := pvc.Status.Capacity[v1.ResourceStorage]
		line := fmt.Sprintf("%-50s %-20s %-15s %-15s",
			pvc.Name,
			pvc.Status.Phase,
			pvc.Spec.VolumeName,
			capacity.String(),
		)

		if i == m.cursor {
			s += m.styles.SelectedItem.Render("> " + line)
		} else {
			s += "  " + line
		}
		s += "\n"
	}
	return s
}
func renderPVs(m model) string {
	s := "PersistentVolumes:\n\n"
	header := fmt.Sprintf("%-50s %-20s %-15s %-15s %-15s", "NAME", "STATUS", "CAPACITY", "CLAIM", "RECLAIM POLICY")
	s += m.styles.TableHeader.Render(header) + "\n"
	for i, pv := range m.pvs {
		capacity := pv.Spec.Capacity[v1.ResourceStorage]
		claim := ""
		if pv.Spec.ClaimRef != nil {
			claim = pv.Spec.ClaimRef.Name
		}
		line := fmt.Sprintf("%-50s %-20s %-15s %-15s %-15s",
			pv.Name,
			pv.Status.Phase,
			capacity.String(),
			claim,
			pv.Spec.PersistentVolumeReclaimPolicy,
		)

		if i == m.cursor {
			s += m.styles.SelectedItem.Render("> " + line)
		} else {
			s += "  " + line
		}
		s += "\n"
	}
	return s
}

func renderDeployments(m model) string {
	s := "Deployments:\n\n"
	header := fmt.Sprintf("%-50s %-15s %-15s %-15s", "NAME", "READY", "UP-TO-DATE", "AVAILABLE")
	s += m.styles.TableHeader.Render(header) + "\n"
	for i, d := range m.deployments {
		line := fmt.Sprintf("%-50s %-15s %-15s %-15s",
			d.Name,
			fmt.Sprintf("%d/%d", d.Status.ReadyReplicas, *d.Spec.Replicas),
			fmt.Sprintf("%d", d.Status.UpdatedReplicas),
			fmt.Sprintf("%d", d.Status.AvailableReplicas),
		)

		if i == m.cursor {
			s += m.styles.SelectedItem.Render("> " + line)
		} else {
			s += "  " + line
		}
		s += "\n"
	}
	return s
}

func renderStatefulSets(m model) string {
	s := "StatefulSets:\n\n"
	header := fmt.Sprintf("%-50s %-15s", "NAME", "READY")
	s += m.styles.TableHeader.Render(header) + "\n"
	for i, ss := range m.statefulsets {
		line := fmt.Sprintf("%-50s %-15s",
			ss.Name,
			fmt.Sprintf("%d/%d", ss.Status.ReadyReplicas, *ss.Spec.Replicas),
		)

		if i == m.cursor {
			s += m.styles.SelectedItem.Render("> " + line)
		} else {
			s += "  " + line
		}
		s += "\n"
	}
	return s
}
func renderDaemonSets(m model) string {
	s := "DaemonSets:\n\n"
	header := fmt.Sprintf("%-50s %-15s %-15s %-15s", "NAME", "DESIRED", "CURRENT", "READY")
	s += m.styles.TableHeader.Render(header) + "\n"
	for i, ds := range m.daemonsets {
		line := fmt.Sprintf("%-50s %-15d %-15d %-15d",
			ds.Name,
			ds.Status.DesiredNumberScheduled,
			ds.Status.CurrentNumberScheduled,
			ds.Status.NumberReady,
		)

		if i == m.cursor {
			s += m.styles.SelectedItem.Render("> " + line)
		} else {
			s += "  " + line
		}
		s += "\n"
	}
	return s
}

func renderServices(m model) string {
	s := "Services:\n\n"
	header := fmt.Sprintf("%-50s %-20s %-20s %-20s", "NAME", "TYPE", "CLUSTER-IP", "PORTS")
	s += m.styles.TableHeader.Render(header) + "\n"
	for i, svc := range m.services {
		ports := []string{}
		for _, p := range svc.Spec.Ports {
			ports = append(ports, fmt.Sprintf("%d/%s", p.Port, p.Protocol))
		}
		line := fmt.Sprintf("%-50s %-20s %-20s %-20s",
			svc.Name,
			svc.Spec.Type,
			svc.Spec.ClusterIP,
			strings.Join(ports, ", "),
		)

		if i == m.cursor {
			s += m.styles.SelectedItem.Render("> " + line)
		} else {
			s += "  " + line
		}
		s += "\n"
	}
	return s
}
func renderNetworkPolicies(m model) string {
	s := "NetworkPolicies:\n\n"
	header := fmt.Sprintf("%-50s", "NAME")
	s += m.styles.TableHeader.Render(header) + "\n"
	for i, p := range m.netpols {
		line := fmt.Sprintf("%-50s", p.Name)
		if i == m.cursor {
			s += m.styles.SelectedItem.Render("> " + line)
		} else {
			s += "  " + line
		}
		s += "\n"
	}
	return s
}
func renderEvents(m model) string {
	s := "Events:\n\n"
	header := fmt.Sprintf("%-20s %-20s %-50s %s", "LAST SEEN", "TYPE", "REASON", "MESSAGE")
	s += m.styles.TableHeader.Render(header) + "\n"
	for i, e := range m.events {
		line := fmt.Sprintf("%-20s %-20s %-50s %s",
			e.LastTimestamp.Format("2006-01-02 15:04:05"),
			e.Type,
			e.Reason,
			e.Message,
		)

		if i == m.cursor {
			s += m.styles.SelectedItem.Render("> " + line)
		} else {
			s += "  " + line
		}
		s += "\n"
	}
	return s
}

func renderNamespaces(m model) string {
	s := "Select a namespace:\n\n"
	namespaces := append([]v1.Namespace{{ObjectMeta: metav1.ObjectMeta{Name: "all"}}}, m.namespaces...)
	for i, ns := range namespaces {
		line := ns.Name
		if i == m.cursor {
			s += m.styles.SelectedItem.Render("> " + line)
		} else {
			s += "  " + line
		}
		s += "\n"
	}
	return s
}

func renderDashboard(m model) string {
	var sb strings.Builder
	sb.WriteString(m.styles.Title.Render("Cluster Dashboard") + "\n\n")

	// Cluster-wide metrics
	sb.WriteString(m.styles.Bold.Render("Cluster-wide Usage:") + "\n")
	sb.WriteString(fmt.Sprintf("CPU: %s\n", m.clusterCPUUsage))
	sb.WriteString(fmt.Sprintf("Memory: %s\n\n", m.clusterMemoryUsage))

	// Chart Section
	m.podCPUChart.Draw()
	m.podMemoryChart.Draw()
	m.nodeCPUChart.Draw()
	m.nodeMemoryChart.Draw()

	podCPUTitle := m.styles.ChartTitle.Render("Top Pods by CPU (mCores)")
	podMemTitle := m.styles.ChartTitle.Render("Top Pods by Memory (MiB)")
	nodeCPUTitle := m.styles.ChartTitle.Render("Top Nodes by CPU (mCores)")
	nodeMemTitle := m.styles.ChartTitle.Render("Top Nodes by Memory (MiB)")

	chartsTop := lipgloss.JoinHorizontal(lipgloss.Top,
		lipgloss.JoinVertical(lipgloss.Left, podCPUTitle, m.podCPUChart.View()),
		lipgloss.JoinVertical(lipgloss.Left, podMemTitle, m.podMemoryChart.View()),
	)
	chartsBottom := lipgloss.JoinHorizontal(lipgloss.Top,
		lipgloss.JoinVertical(lipgloss.Left, nodeCPUTitle, m.nodeCPUChart.View()),
		lipgloss.JoinVertical(lipgloss.Left, nodeMemTitle, m.nodeMemoryChart.View()),
	)

	sb.WriteString(lipgloss.JoinVertical(lipgloss.Left, chartsTop, "\n\n", chartsBottom))

	return sb.String()
}
func renderHostDashboard(m model) string {
	var sb strings.Builder
	sb.WriteString(m.styles.Title.Render("Host-Level Dashboard") + "\n\n")

	// Render Tabs
	var renderedTabs []string
	for i, t := range m.hostTabs {
		if i == m.activeHostTab {
			renderedTabs = append(renderedTabs, m.styles.ActiveTab.Render(t))
		} else {
			renderedTabs = append(renderedTabs, m.styles.Tab.Render(t))
		}
	}
	sb.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, renderedTabs...) + "\n\n")

	switch m.hostTabs[m.activeHostTab] {
	case "Host Metrics":
		sb.WriteString(renderHostMetrics(m))
	case "System Logs":
		left := renderHostLogsMenu(m)
		right := m.viewport.View()
		sb.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, left, right))
	case "Application Logs":
		left := renderAppLogsMenu(m)
		right := m.viewport.View()
		sb.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, left, right))
	}

	return sb.String()
}
func renderHostMetrics(m model) string {
	var sb strings.Builder
	// Charts
	m.hostCPUChart.Draw()
	m.hostMemoryChart.Draw()

	cpuTitle := m.styles.ChartTitle.Render("Host CPU Usage (%)")
	memTitle := m.styles.ChartTitle.Render("Host Memory Usage (%)")

	charts := lipgloss.JoinHorizontal(lipgloss.Top,
		lipgloss.JoinVertical(lipgloss.Left, cpuTitle, m.hostCPUChart.View()),
		lipgloss.JoinVertical(lipgloss.Left, memTitle, m.hostMemoryChart.View()),
	)
	sb.WriteString(charts + "\n\n")

	// Disk Usage
	sb.WriteString(m.styles.Bold.Render("Disk Usage:") + "\n")
	diskHeader := fmt.Sprintf("%-20s %-15s %-15s %-15s %s", "Mountpoint", "Total", "Used", "Free", "Used %")
	sb.WriteString(m.styles.TableHeader.Render(diskHeader) + "\n")
	for _, d := range m.hostDiskUsage {
		sb.WriteString(fmt.Sprintf("%-20s %-15s %-15s %-15s %.2f%%\n",
			d.Mountpoint,
			formatBytes(d.Total),
			formatBytes(d.Used),
			formatBytes(d.Free),
			d.UsedPercent,
		))
	}

	return sb.String()
}
func renderHostLogsMenu(m model) string {
	s := "Select a log type to view:\n\n"
	for i, lt := range m.hostLogTypes {
		if i == m.cursor {
			s += m.styles.SelectedItem.Render("> " + lt)
		} else {
			s += "  " + lt
		}
		s += "\n"
	}
	return s
}
func renderAppLogsMenu(m model) string {
	s := "Select a container to view logs:\n\n"
	if len(m.containers) == 0 {
		return "No running containers found."
	}
	for i, c := range m.containers {
		if i == m.cursor {
			s += m.styles.SelectedItem.Render("> " + c)
		} else {
			s += "  " + c
		}
		s += "\n"
	}
	return s
}

// Formatting functions
func formatNodeDetails(node v1.Node) string {
	return fmt.Sprintf("Name: %s\nStatus: %s\nKubelet Version: %s\nOS: %s\nArchitecture: %s",
		node.Name,
		node.Status.Conditions[len(node.Status.Conditions)-1].Type,
		node.Status.NodeInfo.KubeletVersion,
		node.Status.NodeInfo.OperatingSystem,
		node.Status.NodeInfo.Architecture,
	)
}

func formatPodDetails(pod v1.Pod) string {
	restarts := 0
	for _, cs := range pod.Status.ContainerStatuses {
		restarts += int(cs.RestartCount)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Name:\t%s\n", pod.Name))
	sb.WriteString(fmt.Sprintf("Namespace:\t%s\n", pod.Namespace))
	sb.WriteString(fmt.Sprintf("Status:\t%s\n", pod.Status.Phase))
	sb.WriteString(fmt.Sprintf("Node:\t%s\n", pod.Spec.NodeName))
	sb.WriteString(fmt.Sprintf("IP:\t%s\n", pod.Status.PodIP))
	sb.WriteString(fmt.Sprintf("Restarts:\t%d\n", restarts))
	sb.WriteString(fmt.Sprintf("Controlled By:\t%s\n", pod.OwnerReferences[0].Name))

	// Container Statuses
	sb.WriteString("\nContainers:\n")
	for _, cs := range pod.Status.ContainerStatuses {
		sb.WriteString(fmt.Sprintf("  - Name:\t%s\n", cs.Name))
		sb.WriteString(fmt.Sprintf("    Image:\t%s\n", cs.Image))
		sb.WriteString(fmt.Sprintf("    Ready:\t%t\n", cs.Ready))
		sb.WriteString(fmt.Sprintf("    Restarts:\t%d\n", cs.RestartCount))
	}
	return sb.String()
}

func formatPVCDetails(pvc v1.PersistentVolumeClaim) string {
	capacity := pvc.Status.Capacity[v1.ResourceStorage]
	return fmt.Sprintf("Name: %s\nNamespace: %s\nStatus: %s\nVolume: %s\nCapacity: %s",
		pvc.Name,
		pvc.Namespace,
		pvc.Status.Phase,
		pvc.Spec.VolumeName,
		(&capacity).String(),
	)
}
func formatPVDetails(pv v1.PersistentVolume) string {
	capacity := pv.Spec.Capacity[v1.ResourceStorage]
	return fmt.Sprintf("Name: %s\nStatus: %s\nCapacity: %s\nClaim: %s\nReclaim Policy: %s",
		pv.Name,
		pv.Status.Phase,
		(&capacity).String(),
		pv.Spec.ClaimRef.Name,
		pv.Spec.PersistentVolumeReclaimPolicy,
	)
}
func formatDeploymentDetails(d appsv1.Deployment) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Name:\t%s\n", d.Name))
	sb.WriteString(fmt.Sprintf("Namespace:\t%s\n", d.Namespace))
	sb.WriteString(fmt.Sprintf("Replicas:\t%d/%d\n", d.Status.ReadyReplicas, *d.Spec.Replicas))
	sb.WriteString(fmt.Sprintf("Strategy:\t%s\n", d.Spec.Strategy.Type))
	sb.WriteString(fmt.Sprintf("Last Update:\t%s\n", d.Status.Conditions[0].LastUpdateTime.Format("2006-01-02 15:04:05")))
	return sb.String()
}
func formatStatefulSetDetails(ss appsv1.StatefulSet) string {
	return fmt.Sprintf("Name: %s\nNamespace: %s\nReplicas: %d/%d",
		ss.Name,
		ss.Namespace,
		ss.Status.ReadyReplicas,
		*ss.Spec.Replicas,
	)
}
func formatDaemonSetDetails(ds appsv1.DaemonSet) string {
	return fmt.Sprintf("Name: %s\nNamespace: %s\nDesired: %d\nCurrent: %d\nReady: %d",
		ds.Name,
		ds.Namespace,
		ds.Status.DesiredNumberScheduled,
		ds.Status.CurrentNumberScheduled,
		ds.Status.NumberReady,
	)
}

func formatServiceDetails(svc v1.Service) string {
	return fmt.Sprintf("Name: %s\nNamespace: %s\nType: %s\nClusterIP: %s",
		svc.Name,
		svc.Namespace,
		svc.Spec.Type,
		svc.Spec.ClusterIP,
	)
}
func formatNetworkPolicyDetails(p networkingv1.NetworkPolicy) string {
	return fmt.Sprintf("Name: %s\nNamespace: %s",
		p.Name,
		p.Namespace,
	)
}
func formatEventDetails(e v1.Event) string {
	return fmt.Sprintf("Reason: %s\nMessage: %s\nSource: %s\nLast Seen: %s",
		e.Reason,
		e.Message,
		e.Source.Component,
		e.LastTimestamp.Format("2006-01-02 15:04:05"),
	)
}
func totalPodCPU(metrics v1beta1.PodMetrics) *resource.Quantity {
	total := resource.NewQuantity(0, resource.DecimalSI)
	for _, c := range metrics.Containers {
		total.Add(c.Usage[v1.ResourceCPU])
	}
	return total
}

func totalPodMemory(metrics v1beta1.PodMetrics) *resource.Quantity {
	total := resource.NewQuantity(0, resource.BinarySI)
	for _, c := range metrics.Containers {
		total.Add(c.Usage[v1.ResourceMemory])
	}
	return total
}

func formatMilliCPU(q *resource.Quantity) string {
	if q == nil {
		return "N/A"
	}
	return fmt.Sprintf("%dm", q.MilliValue())
}

func formatMiBMemory(q *resource.Quantity) string {
	if q == nil {
		return "N/A"
	}
	return fmt.Sprintf("%dMi", q.Value()/(1024*1024))
}
func formatPercentage(used, total int64) string {
	if total == 0 {
		return "0"
	}
	return fmt.Sprintf("%.2f", float64(used)/float64(total)*100)
}
func formatBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}

// Main function
func main() {
	kubeconfig := flag.String("kubeconfig", filepath.Join(os.Getenv("HOME"), ".kube", "config"), "absolute path to the kubeconfig file")
	flag.Parse()

	config, err := clientcmd.BuildConfigFromFlags("", *kubeconfig)
	if err != nil {
		panic(err.Error())
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		panic(err.Error())
	}

	metricsClientset, err := metrics.NewForConfig(config)
	if err != nil {
		panic(err.Error())
	}

	m := initialModel(clientset, metricsClientset)
	p := tea.NewProgram(m, tea.WithAltScreen())

	if err := p.Start(); err != nil {
		fmt.Printf("Alas, there's been an error: %v", err)
		os.Exit(1)
	}
}

func initialModel(clientset *kubernetes.Clientset, metricsClientset *metrics.Clientset) model {
	styles := DefaultStyles()
	resourceTypes := []string{"Cluster Dashboard", "Host Dashboard", "Namespaces", "Nodes", "Pods", "Deployments", "StatefulSets", "DaemonSets", "Services", "PersistentVolumeClaims", "PersistentVolumes", "NetworkPolicies", "Events"}
	hostLogTypes := []string{"System Logs", "Kubelet Logs", "Docker Logs", "dmesg"}

	return model{
		clientset:        clientset,
		metricsClientset: metricsClientset,
		resourceTypes:    resourceTypes,
		hostLogTypes:     hostLogTypes,
		view:             viewResourceMenu,
		styles:           styles,
		textInput:        newTextInput(),
		podCPUChart:      barchart.New(40, 10),
		podMemoryChart:   barchart.New(40, 10),
		nodeCPUChart:     barchart.New(40, 10),
		nodeMemoryChart:  barchart.New(40, 10),
		hostTabs:         []string{"Host Metrics", "System Logs", "Application Logs"},
		hostCPUChart:     barchart.New(40, 10),
		hostMemoryChart:  barchart.New(40, 10),
	}
}

func newTextInput() textinput.Model {
	ti := textinput.New()
	ti.Placeholder = "1"
	ti.Focus()
	ti.CharLimit = 3
	ti.Width = 20
	ti.Prompt = "Enter replicas: "
	return ti
}
