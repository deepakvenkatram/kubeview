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
	"net/smtp"
	"strings"
	"time"

	"github.com/NimbleMarkets/ntcharts/barchart"
	"github.com/charmbracelet/bubbles/textarea"
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
	"k8s.io/apimachinery/pkg/types"
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
	viewAlerts
	viewResourceQuotas
	viewEdit
	viewWorkloads
	viewStorage
	viewNetworkMenu
	viewCluster
	viewSMTPConfig
	viewHost
	viewHostLogsMenu
	viewHostLogs
)

type resourceQuotaLine struct {
	Name     string
	Resource string
	Used     string
	Limit    string
}

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
	alerts             []v1.Event
	resourcequotas     []v1.ResourceQuota
	resourceQuotaLines []resourceQuotaLine
	namespaces         []v1.Namespace
	resourceTypes      []string
	workloadTypes      []string
	storageTypes       []string
	networkTypes       []string
	clusterTypes       []string
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
	editor             textarea.Model
	smtpServer         textinput.Model
	smtpPort           textinput.Model
	smtpUsername       textinput.Model
	smtpPassword       textinput.Model
	recipientEmail     textinput.Model
	statusMessage      string
	statusMessageTimer *time.Timer
	hostCPUUsage       string
	hostMemoryUsage    string
	hostDiskUsage      []diskUsageStat
	ready              bool
}

type tickMsg time.Time
type hostMsg struct {
	cpuUsage    string
	memoryUsage string
	diskUsage   []diskUsageStat
}
type hostLogsMsg struct{ logs string }
type logsMsg struct{ logs string }
type emailSentMsg struct{}
type clearStatusMsg struct{}
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
type alertsMsg struct{ alerts []v1.Event }
type resourceQuotasMsg struct{ quotas []v1.ResourceQuota }
type patchMsg struct{}
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

func patchDeployment(clientset *kubernetes.Clientset, namespace, name, patch string) tea.Cmd {
	return func() tea.Msg {
		_, err := clientset.AppsV1().Deployments(namespace).Patch(context.Background(), name, types.StrategicMergePatchType, []byte(patch), metav1.PatchOptions{})
		if err != nil {
			return errMsg{err}
		}
		return patchMsg{}
	}
}

func patchResourceQuota(clientset *kubernetes.Clientset, namespace, name, patch string) tea.Cmd {
	return func() tea.Msg {
		_, err := clientset.CoreV1().ResourceQuotas(namespace).Patch(context.Background(), name, types.StrategicMergePatchType, []byte(patch), metav1.PatchOptions{})
		if err != nil {
			return errMsg{err}
		}
		return patchMsg{}
	}
}

func getHostMetrics() tea.Cmd {
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

func sendEmail(server, port, username, password, recipient, subject, body string) tea.Cmd {
	return func() tea.Msg {
		auth := smtp.PlainAuth("", username, password, server)
		to := []string{recipient}
		msg := []byte("To: " + recipient + "\r\n" +
			"Subject: " + subject + "\r\n" +
			"\r\n" +
			body + "\r\n")
		err := smtp.SendMail(server+":"+port, auth, username, to, msg)
		if err != nil {
			return errMsg{err}
		}
		return emailSentMsg{}
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
		var cmd string
		switch logType {
		case "System Logs":
			cmd = "journalctl"
		case "Kubelet Logs":
			cmd = "journalctl -u kubelet.service"
		case "Docker Logs":
			cmd = "journalctl -u docker.service"
		default:
			return errMsg{fmt.Errorf("unknown log type: %s", logType)}
		}

		// Since we're executing a command, we'll use os/exec
		c := exec.Command("bash", "-c", cmd)
		var out bytes.Buffer
		c.Stdout = &out
		err := c.Run()
		if err != nil {
			return errMsg{err}
		}
		return hostLogsMsg{logs: out.String()}
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

func getAlerts(clientset *kubernetes.Clientset, namespace string) tea.Cmd {
	return func() tea.Msg {
		options := metav1.ListOptions{
			FieldSelector: "type=Warning",
		}
		events, err := clientset.CoreV1().Events(namespace).List(context.Background(), options)
		if err != nil {
			return errMsg{err}
		}

		alerts := events.Items
		sort.Slice(alerts, func(i, j int) bool {
			return alerts[i].LastTimestamp.Time.After(alerts[j].LastTimestamp.Time)
		})
		return alertsMsg{alerts}
	}
}

func getResourceQuotas(clientset *kubernetes.Clientset, namespace string) tea.Cmd {
	return func() tea.Msg {
		quotas, err := clientset.CoreV1().ResourceQuotas(namespace).List(context.Background(), metav1.ListOptions{})
		if err != nil {
			return errMsg{err}
		}
		return resourceQuotasMsg{quotas.Items}
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
	return tea.Batch(getNodes(m.clientset, m.metricsClientset), doTick())
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var (
		cmd  tea.Cmd
		cmds []tea.Cmd
	)

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		headerHeight := lipgloss.Height(m.headerView())
		footerHeight := lipgloss.Height(m.footerView())
		verticalMarginHeight := headerHeight + footerHeight

		if !m.ready {
			m.viewport = viewport.New(msg.Width, msg.Height-verticalMarginHeight)
			m.viewport.YPosition = headerHeight
			m.ready = true
		} else {
			m.viewport.Width = msg.Width
			m.viewport.Height = msg.Height - verticalMarginHeight
		}
		m.editor.SetWidth(msg.Width)
		chartHeight := (msg.Height - verticalMarginHeight) / 4
		m.podCPUChart = barchart.New(msg.Width, chartHeight)
		m.podMemoryChart = barchart.New(msg.Width, chartHeight)
		m.nodeCPUChart = barchart.New(msg.Width, chartHeight)
		m.nodeMemoryChart = barchart.New(msg.Width, chartHeight)
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
		case viewAlerts:
			return m, getAlerts(m.clientset, m.selectedNamespace)
		case viewResourceQuotas:
			return m, getResourceQuotas(m.clientset, m.selectedNamespace)
		case viewDashboard:
			return m, getDashboardMetrics(m.clientset, m.metricsClientset, m.styles)
		case viewHost:
			return m, getHostMetrics()
		}
		return m, doTick()
	case hostMsg:
		m.hostCPUUsage = msg.cpuUsage
		m.hostMemoryUsage = msg.memoryUsage
		m.hostDiskUsage = msg.diskUsage
		return m, nil
	case emailSentMsg:
		if m.statusMessageTimer != nil {
			m.statusMessageTimer.Stop()
		}
		m.statusMessage = "Email sent successfully!"
		m.statusMessageTimer = time.NewTimer(3 * time.Second)
		return m, func() tea.Msg {
			<-m.statusMessageTimer.C
			return clearStatusMsg{}
		}
	case clearStatusMsg:
		m.statusMessage = ""
		return m, nil
	case hostLogsMsg:
		m.viewport.SetContent(msg.logs)
		m.view = viewHostLogs
		return m, nil
	case logsMsg:
		m.viewport.SetContent(msg.logs)
		m.view = viewLogs
		return m, nil
	case scaleMsg:
		m.view = viewDetails
		return m, getDeployments(m.clientset, m.selectedNamespace)
	case patchMsg:
		m.view = viewDetails
		if m.previousView == viewDeployments {
			return m, getDeployments(m.clientset, m.selectedNamespace)
		}
		if m.previousView == viewResourceQuotas {
			return m, getResourceQuotas(m.clientset, m.selectedNamespace)
		}
		return m, nil
	case podDeletedMsg:
		m.view = viewPods
		return m, getPods(m.clientset, m.metricsClientset, m.selectedNamespace)
	case namespacesMsg:
		m.namespaces = msg.namespaces
		m.cursor = 0
		return m, nil
	case nodesMsg:
		m.nodes = msg.nodes
		m.nodeMetrics = msg.metrics
		if m.cursor >= len(m.nodes) {
			m.cursor = 0
		}
		return m, doTick()
	case podsMsg:
		m.pods = msg.pods
		m.podMetrics = msg.metrics
		if m.cursor >= len(m.pods) {
			m.cursor = 0
		}
		return m, doTick()
	case pvcsMsg:
		m.pvcs = msg.pvcs
		m.cursor = 0
		return m, doTick()
	case pvsMsg:
		m.pvs = msg.pvs
		m.cursor = 0
		return m, doTick()
	case deploymentsMsg:
		m.deployments = msg.deployments
		m.cursor = 0
		return m, doTick()
	case statefulsetsMsg:
		m.statefulsets = msg.statefulsets
		m.cursor = 0
		return m, doTick()
	case daemonsetsMsg:
		m.daemonsets = msg.daemonsets
		m.cursor = 0
		return m, doTick()
	case servicesMsg:
		m.services = msg.services
		m.cursor = 0
		return m, doTick()
	case networkPoliciesMsg:
		m.netpols = msg.policies
		m.cursor = 0
		return m, doTick()
	case eventsMsg:
		m.events = msg.events
		m.cursor = 0
		return m, doTick()
	case alertsMsg:
		m.alerts = msg.alerts
		m.cursor = 0
		return m, doTick()
	case resourceQuotasMsg:
		m.resourcequotas = msg.quotas
		var lines []resourceQuotaLine
		for _, rq := range msg.quotas {
			for resourceName, used := range rq.Status.Used {
				limit, ok := rq.Spec.Hard[resourceName]
				if !ok {
					limit = resource.MustParse("0")
				}
				lines = append(lines, resourceQuotaLine{
					Name:     rq.Name,
					Resource: string(resourceName),
					Used:     used.String(),
					Limit:    limit.String(),
				})
			}
		}
		m.resourceQuotaLines = lines
		m.cursor = 0
		return m, doTick()
	case errMsg:
		m.err = msg
		return m, nil // Don't quit, just store the error and let the View function display it
	case yamlMsg: // New case
		m.yamlContent = msg.yaml
		m.view = viewYAML
		return m, nil
	case dashboardMsg: // New case for dashboard metrics
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
		m.podCPUChart.Draw()
		m.podMemoryChart.Draw()
		m.nodeCPUChart.Draw()
		m.nodeMemoryChart.Draw()
		return m, doTick()
	case tea.KeyMsg:
		if m.view == viewConfirmDelete {
			switch msg.String() {
			case "y", "Y":
				pod := m.pods[m.cursor]
				return m, deletePod(m.clientset, pod.Namespace, pod.Name)
			case "n", "N", "esc":
				m.view = viewDetails
			}
			return m, nil
		}
		if m.view == viewScaling {
			switch msg.String() {
			case "enter":
				replicaCount, err := strconv.Atoi(m.textInput.Value())
				if err == nil {
					d := m.deployments[m.cursor]
					return m, scaleDeployment(m.clientset, d.Namespace, d.Name, int32(replicaCount))
				}
			case "esc":
				m.view = viewDetails
				m.textInput.Reset()
			default:
				m.textInput, cmd = m.textInput.Update(msg)
				cmds = append(cmds, cmd)
			}
			return m, tea.Batch(cmds...)
		}
		if m.view == viewEdit {
			switch msg.Type {
			case tea.KeyCtrlS:
				if m.previousView == viewDeployments {
					d := m.deployments[m.cursor]
					return m, patchDeployment(m.clientset, d.Namespace, d.Name, m.editor.Value())
				}
				if m.previousView == viewResourceQuotas {
					rq := m.resourcequotas[m.cursor]
					return m, patchResourceQuota(m.clientset, rq.Namespace, rq.Name, m.editor.Value())
				}
			case tea.KeyEsc:
				m.view = viewDetails
				m.editor.Reset()
				return m, nil
			}
			var cmd tea.Cmd
			m.editor, cmd = m.editor.Update(msg)
			cmds = append(cmds, cmd)
			return m, tea.Batch(cmds...)
		}
		if m.view == viewSMTPConfig {
			switch msg.String() {
			case "enter":
				subject := "KubeView Test Email"
				body := "This is a test email from KubeView to confirm your SMTP settings are correct."
				m.view = viewAlerts
				return m, sendEmail(m.smtpServer.Value(), m.smtpPort.Value(), m.smtpUsername.Value(), m.smtpPassword.Value(), m.recipientEmail.Value(), subject, body)
			case "esc":
				m.view = viewAlerts
			default:
				var cmd tea.Cmd
				if m.smtpServer.Focused() {
					m.smtpServer, cmd = m.smtpServer.Update(msg)
				} else if m.smtpPort.Focused() {
					m.smtpPort, cmd = m.smtpPort.Update(msg)
				} else if m.smtpUsername.Focused() {
					m.smtpUsername, cmd = m.smtpUsername.Update(msg)
				} else if m.smtpPassword.Focused() {
					m.smtpPassword, cmd = m.smtpPassword.Update(msg)
				} else if m.recipientEmail.Focused() {
					m.recipientEmail, cmd = m.recipientEmail.Update(msg)
				}
				cmds = append(cmds, cmd)
			}
			return m, tea.Batch(cmds...)
		}
		if m.view == viewLogs {
			switch msg.String() {
			case "esc", "backspace", "q":
				m.view = viewDetails
			default:
				m.viewport, cmd = m.viewport.Update(msg)
				cmds = append(cmds, cmd)
			}
			return m, tea.Batch(cmds...)
		}
		if m.view == viewHostLogs {
			switch msg.String() {
			case "esc", "backspace", "q":
				m.view = viewHostLogsMenu
			default:
				m.viewport, cmd = m.viewport.Update(msg)
				cmds = append(cmds, cmd)
			}
			return m, tea.Batch(cmds...)
		}
		if m.view == viewYAML { // New view for YAML
			switch msg.String() {
			case "esc", "backspace", "q":
				m.view = viewDetails
			default:
				m.viewport, cmd = m.viewport.Update(msg)
				cmds = append(cmds, cmd)
			}
			return m, tea.Batch(cmds...)
		}
		if m.view == viewAlerts {
			switch msg.String() {
			case "c":
				m.previousView = viewAlerts
				m.view = viewSMTPConfig
				m.smtpServer.Focus()
				return m, nil
			}
		}
		if m.view == viewDetails && m.previousView == viewAlerts {
			switch msg.String() {
			case "s":
				alert := m.alerts[m.cursor]
				subject := fmt.Sprintf("KubeView Alert: %s", alert.Reason)
				body := m.formatEventDetails(alert)
				return m, sendEmail(m.smtpServer.Value(), m.smtpPort.Value(), m.smtpUsername.Value(), m.smtpPassword.Value(), m.recipientEmail.Value(), subject, body)
			}
		}
		if m.view == viewDetails {
			switch msg.String() {
			case "d":
				if m.previousView == viewPods {
					m.view = viewConfirmDelete
					return m, nil
				}
			case "r":
				if m.previousView == viewDeployments {
					m.view = viewScaling
					m.textInput.Focus()
					m.textInput.SetValue(fmt.Sprintf("%d", *m.deployments[m.cursor].Spec.Replicas))
					return m, nil
				}
			case "e":
				if m.previousView == viewDeployments {
					m.view = viewEdit
					d := m.deployments[m.cursor]
					s := json.NewYAMLSerializer(json.DefaultMetaFactory, scheme.Scheme, scheme.Scheme)
					var b bytes.Buffer
					if err := s.Encode(&d, &b); err == nil {
						m.editor.SetValue(b.String())
					}
					m.editor.Focus()
					return m, nil
				}
				if m.previousView == viewResourceQuotas {
					m.view = viewEdit
					rq := m.resourcequotas[m.cursor]
					s := json.NewYAMLSerializer(json.DefaultMetaFactory, scheme.Scheme, scheme.Scheme)
					var b bytes.Buffer
					if err := s.Encode(&rq, &b); err == nil {
						m.editor.SetValue(b.String())
					}
					m.editor.Focus()
					return m, nil
				}
			case "l":
				if m.previousView == viewPods {
					pod := m.pods[m.cursor]
					return m, getLogs(m.clientset, pod.Namespace, pod.Name)
				}
			case "y": // New keybinding for YAML
				var name, namespace, kind string
				switch m.previousView {
				case viewNodes:
					name = m.nodes[m.cursor].Name
					kind = "Node"
				case viewPods:
					name = m.pods[m.cursor].Name
					namespace = m.pods[m.cursor].Namespace
					kind = "Pod"
				case viewPVCs:
					name = m.pvcs[m.cursor].Name
					namespace = m.pvcs[m.cursor].Namespace
					kind = "PersistentVolumeClaim"
				case viewPVs:
					name = m.pvs[m.cursor].Name
					kind = "PersistentVolume"
				case viewDeployments:
					name = m.deployments[m.cursor].Name
					namespace = m.deployments[m.cursor].Namespace
					kind = "Deployment"
				case viewStatefulSets:
					name = m.statefulsets[m.cursor].Name
					namespace = m.statefulsets[m.cursor].Namespace
					kind = "StatefulSet"
				case viewDaemonSets:
					name = m.daemonsets[m.cursor].Name
					namespace = m.daemonsets[m.cursor].Namespace
					kind = "DaemonSet"
				case viewServices:
					name = m.services[m.cursor].Name
					namespace = m.services[m.cursor].Namespace
					kind = "Service"
				case viewNetworkPolicies:
					name = m.netpols[m.cursor].Name
					namespace = m.netpols[m.cursor].Namespace
					kind = "NetworkPolicy"
				case viewEvents:
					name = m.events[m.cursor].Name
					namespace = m.events[m.cursor].Namespace
					kind = "Event"
				case viewNamespaces:
					// Namespaces don't have a specific YAML view in this context,
					// or it's less common to view their YAML directly from a list.
					// For now, we can skip or add later if needed.
					return m, nil
				default:
					return m, nil
				}
				return m, getResourceYAML(m.clientset, namespace, name, kind)
			case "esc", "backspace":
				m.view = m.previousView
			}
			return m, nil
		}
		if m.view == viewHelp {
			switch msg.String() {
			case "l":
				m.previousView = m.view
				m.view = viewHostLogsMenu
				m.cursor = 0
				return m, nil
			case "esc", "backspace":
				m.view = m.previousView
			}
			return m, nil
		}
		if m.view == viewHostLogsMenu {
			switch msg.String() {
			case "enter":
				logType := m.hostLogTypes[m.cursor]
				return m, getHostLogs(logType)
			case "esc", "backspace":
				m.view = viewHost
				m.cursor = 0
			case "up":
				if m.cursor > 0 {
					m.cursor--
				}
			case "down":
				if m.cursor < len(m.hostLogTypes)-1 {
					m.cursor++
				}
			}
			return m, nil
		}
		if m.view == viewNamespaces {
			switch msg.String() {
			case "enter":
				if m.cursor == 0 {
					m.selectedNamespace = "" // All namespaces
				} else {
					m.selectedNamespace = m.namespaces[m.cursor-1].Name
				}
				m.view = m.previousView
				updatedModel, cmd := m.Update(tickMsg{})
				return updatedModel, cmd
			case "esc", "backspace", "N":
				m.view = m.previousView
			case "up":
				if m.cursor > 0 {
					m.cursor--
				}
			case "down":
				if m.cursor < len(m.namespaces) {
					m.cursor++
				}
			}
			return m, nil
		}
		if m.view == viewResourceMenu {
			switch msg.String() {
			case "enter":
				selectedResource := m.resourceTypes[m.cursor]
				switch selectedResource {
				case "Workloads":
					m.view = viewWorkloads
					m.cursor = 0
					return m, nil
				case "Storage":
					m.view = viewStorage
					m.cursor = 0
					return m, nil
				case "Network":
					m.view = viewNetworkMenu
					m.cursor = 0
					return m, nil
				case "Cluster":
					m.view = viewCluster
					m.cursor = 0
					return m, nil
				}
			case "esc", "backspace", "r":
				m.view = m.previousView
			case "up":
				if m.cursor > 0 {
					m.cursor--
				}
			case "down":
				if m.cursor < len(m.resourceTypes)-1 {
					m.cursor++
				}
			}
			return m, nil
		}

		if m.view == viewWorkloads {
			switch msg.String() {
			case "enter":
				selectedResource := m.workloadTypes[m.cursor]
				switch selectedResource {
				case "Pods":
					m.view = viewPods
					return m, getPods(m.clientset, m.metricsClientset, m.selectedNamespace)
				case "Deployments":
					m.view = viewDeployments
					return m, getDeployments(m.clientset, m.selectedNamespace)
				case "StatefulSets":
					m.view = viewStatefulSets
					return m, getStatefulSets(m.clientset, m.selectedNamespace)
				case "DaemonSets":
					m.view = viewDaemonSets
					return m, getDaemonSets(m.clientset, m.selectedNamespace)
				}
			case "esc", "backspace":
				m.view = viewResourceMenu
				m.cursor = 0
				return m, nil
			case "up":
				if m.cursor > 0 {
					m.cursor--
				}
			case "down":
				if m.cursor < len(m.workloadTypes)-1 {
					m.cursor++
				}
			}
			return m, nil
		}

		if m.view == viewStorage {
			switch msg.String() {
			case "enter":
				selectedResource := m.storageTypes[m.cursor]
				switch selectedResource {
				case "PVCs":
					m.view = viewPVCs
					return m, getPVCs(m.clientset, m.selectedNamespace)
				case "PVs":
					m.view = viewPVs
					return m, getPVs(m.clientset)
				}
			case "esc", "backspace":
				m.view = viewResourceMenu
				m.cursor = 0
				return m, nil
			case "up":
				if m.cursor > 0 {
					m.cursor--
				}
			case "down":
				if m.cursor < len(m.storageTypes)-1 {
					m.cursor++
				}
			}
			return m, nil
		}

		if m.view == viewNetworkMenu {
			switch msg.String() {
			case "enter":
				selectedResource := m.networkTypes[m.cursor]
				switch selectedResource {
				case "Services":
					m.view = viewServices
					return m, getServices(m.clientset, m.selectedNamespace)
				case "Network Policies":
					m.view = viewNetworkPolicies
					return m, getNetworkPolicies(m.clientset, m.selectedNamespace)
				}
			case "esc", "backspace":
				m.view = viewResourceMenu
				m.cursor = 0
				return m, nil
			case "up":
				if m.cursor > 0 {
					m.cursor--
				}
			case "down":
				if m.cursor < len(m.networkTypes)-1 {
					m.cursor++
				}
			}
			return m, nil
		}

		if m.view == viewCluster {
			switch msg.String() {
			case "enter":
				selectedResource := m.clusterTypes[m.cursor]
				switch selectedResource {
				case "Nodes":
					m.view = viewNodes
					return m, getNodes(m.clientset, m.metricsClientset)
				case "Namespaces":
					m.previousView = viewCluster
					m.view = viewNamespaces
					return m, getNamespaces(m.clientset)
				case "Events":
					m.view = viewEvents
					return m, getEvents(m.clientset, m.selectedNamespace)
				case "Alerts":
					m.view = viewAlerts
					return m, getAlerts(m.clientset, m.selectedNamespace)
				case "Resource Quotas":
					m.view = viewResourceQuotas
					return m, getResourceQuotas(m.clientset, m.selectedNamespace)
				}
			case "esc", "backspace":
				m.view = viewResourceMenu
				m.cursor = 0
				return m, nil
			case "up":
				if m.cursor > 0 {
					m.cursor--
				}
			case "down":
				if m.cursor < len(m.clusterTypes)-1 {
					m.cursor++
				}
			}
			return m, nil
		}

		switch msg.String() {
		case "?":
			m.previousView = m.view
			m.view = viewHelp
			return m, nil
		case "q", "ctrl+c":
			return m, tea.Quit
		case "r":
			m.previousView = m.view
			m.view = viewResourceMenu
			m.cursor = 0 // Reset cursor for the new menu
			return m, nil
		case "N":
			m.previousView = m.view
			m.view = viewNamespaces
			return m, getNamespaces(m.clientset)
		case "D": // New keybinding for Dashboard
			m.previousView = m.view
			m.view = viewDashboard
			return m, getDashboardMetrics(m.clientset, m.metricsClientset, m.styles)
		case "H":
			m.previousView = m.view
			m.view = viewHost
			return m, nil
		case "A":
			m.previousView = m.view
			m.view = viewAlerts
			return m, getAlerts(m.clientset, m.selectedNamespace)
		case "Q":
			m.previousView = m.view
			m.view = viewResourceQuotas
			return m, getResourceQuotas(m.clientset, m.selectedNamespace)
		case "up":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			listLen := 0
			switch m.view {
			case viewNodes:
				listLen = len(m.nodes)
			case viewPods:
				listLen = len(m.pods)
			case viewPVCs:
				listLen = len(m.pvcs)
			case viewPVs:
				listLen = len(m.pvs)
			case viewDeployments:
				listLen = len(m.deployments)
			case viewStatefulSets:
				listLen = len(m.statefulsets)
			case viewDaemonSets:
				listLen = len(m.daemonsets)
			case viewServices:
				listLen = len(m.services)
			case viewNetworkPolicies:
				listLen = len(m.netpols)
			case viewEvents:
				listLen = len(m.events)
			case viewResourceQuotas:
				listLen = len(m.resourceQuotaLines)
			}
			if m.cursor < listLen-1 {
				m.cursor++
			}
		case "enter":
			m.previousView = m.view
			m.view = viewDetails
			switch m.previousView {
			case viewNodes:
				node := m.nodes[m.cursor]
				metrics, hasMetrics := m.nodeMetrics[node.Name]
				m.details = m.formatNodeDetails(node, metrics, hasMetrics)
			case viewPods:
				pod := m.pods[m.cursor]
				metrics, hasMetrics := m.podMetrics[pod.Name]
				m.details = m.formatPodDetails(pod, metrics, hasMetrics)
			case viewPVCs:
				m.details = m.formatPVCDetails(m.pvcs[m.cursor])
			case viewPVs:
				m.details = m.formatPVDetails(m.pvs[m.cursor])
			case viewDeployments:
				m.details = m.formatDeploymentDetails(m.deployments[m.cursor])
			case viewStatefulSets:
				m.details = m.formatStatefulSetDetails(m.statefulsets[m.cursor])
			case viewDaemonSets:
				m.details = m.formatDaemonSetDetails(m.daemonsets[m.cursor])
			case viewServices:
				m.details = m.formatServiceDetails(m.services[m.cursor])
			case viewNetworkPolicies:
				m.details = m.formatNetworkPolicyDetails(m.netpols[m.cursor])
			case viewEvents:
				m.details = m.formatEventDetails(m.events[m.cursor])
			case viewAlerts:
				m.details = m.formatEventDetails(m.alerts[m.cursor])
			case viewResourceQuotas:
				selectedLine := m.resourceQuotaLines[m.cursor]
				for _, rq := range m.resourcequotas {
					if rq.Name == selectedLine.Name {
						m.details = m.formatResourceQuotaDetails(rq)
						break
					}
				}
			}
			return m, nil
		}
	}

	if m.view == viewDashboard {
		var cmd tea.Cmd
		m.podCPUChart, cmd = m.podCPUChart.Update(msg)
		cmds = append(cmds, cmd)
		m.podMemoryChart, cmd = m.podMemoryChart.Update(msg)
		cmds = append(cmds, cmd)
		m.nodeCPUChart, cmd = m.nodeCPUChart.Update(msg)
		cmds = append(cmds, cmd)
		m.nodeMemoryChart, cmd = m.nodeMemoryChart.Update(msg)
		cmds = append(cmds, cmd)
	}
	return m, tea.Batch(cmds...)
}

func (m model) headerView() string {
	var title string
	nsText := "all namespaces"
	if m.selectedNamespace != "" {
		nsText = m.selectedNamespace
	}

	switch m.view {
	case viewNodes:
		title = "Nodes"
	case viewPods:
		title = fmt.Sprintf("Pods in %s", nsText)
	case viewPVCs:
		title = fmt.Sprintf("PVCs in %s", nsText)
	case viewPVs:
		title = "PersistentVolumes"
	case viewDeployments:
		title = fmt.Sprintf("Deployments in %s", nsText)
	case viewStatefulSets:
		title = fmt.Sprintf("StatefulSets in %s", nsText)
	case viewDaemonSets:
		title = fmt.Sprintf("DaemonSets in %s", nsText)
	case viewServices:
		title = fmt.Sprintf("Services in %s", nsText)
	case viewNetworkPolicies:
		title = fmt.Sprintf("Network Policies in %s", nsText)
	case viewEvents:
		title = fmt.Sprintf("Events in %s", nsText)
	case viewAlerts:
		title = fmt.Sprintf("Alerts in %s", nsText)
	case viewResourceQuotas:
		title = fmt.Sprintf("Resource Quotas in %s", nsText)
	case viewNamespaces:
		title = "Select Namespace"
	case viewResourceMenu:
		title = "Select Resource Category"
	case viewWorkloads:
		title = "Select Workload"
	case viewStorage:
		title = "Select Storage Resource"
	case viewNetworkMenu:
		title = "Select Network Resource"
	case viewCluster:
		title = "Select Cluster Resource"
	case viewSMTPConfig:
		title = "Configure SMTP"
	case viewHelp:
		title = "Help"
	case viewDetails:
		title = "Details"
	case viewEdit:
		if m.previousView == viewDeployments {
			d := m.deployments[m.cursor]
			title = fmt.Sprintf("Editing Deployment: %s", d.Name)
		}
		if m.previousView == viewResourceQuotas {
			rq := m.resourcequotas[m.cursor]
			title = fmt.Sprintf("Editing Resource Quota: %s", rq.Name)
		}
	case viewLogs:
		pod := m.pods[m.cursor]
		title = fmt.Sprintf("Logs for %s", pod.Name)
	case viewScaling:
		d := m.deployments[m.cursor]
		title = fmt.Sprintf("Scale Deployment: %s", d.Name)
	case viewConfirmDelete:
		p := m.pods[m.cursor]
		title = fmt.Sprintf("Delete Pod: %s", p.Name)
	case viewYAML:
		title = "YAML Details"
	case viewDashboard: // New case
		title = "Cluster Dashboard"
	case viewHost:
		title = "Host Metrics"
	case viewHostLogsMenu:
		title = "Select Host Log Type"
	case viewHostLogs:
		title = "Host Logs"
	}
	return m.styles.HeaderText.Render(title)
}

func (m model) footerView() string {
	if m.view == viewHelp {
		return m.styles.Muted.Render("(esc) back")
	}

	help := "(q)uit | (r)esources | (D)ash | (H)ost | (A)lerts | (N)s | (Q)uotas | (?) help"
	if m.view == viewHost {
		help += " | (l)ogs"
	}
	if m.view == viewDetails {
		baseHelp := "(esc) back"
		switch m.previousView {
		case viewAlerts:
			baseHelp += " | (s)end email | (y)aml"
		case viewPods:
			baseHelp += " | (l)ogs | (d)elete | (y)aml"
		case viewDeployments:
			baseHelp += " | (r)eplicas | (e)dit | (y)aml"
		case viewResourceQuotas:
			baseHelp += " | (e)dit | (y)aml"
		default:
			baseHelp += " | (y)aml"
		}
		help = baseHelp
	}
	if m.view == viewLogs {
		help = "(esc) back to details"
	}
	if m.view == viewYAML {
		help = "(esc) back to details"
	}
	if m.view == viewScaling {
		help = "(enter) confirm | (esc) cancel"
	}
	if m.view == viewEdit {
		help = "(ctrl+s) save | (esc) cancel"
	}
	if m.view == viewConfirmDelete {
		help = "(y)es / (n)o"
	}
	if m.view == viewResourceMenu {
		help = "(enter) select | (esc) back"
	}

	status := ""
	if m.statusMessage != "" {
		status = m.styles.Success.Render(m.statusMessage)
	}

	return lipgloss.JoinVertical(lipgloss.Left, status, m.styles.Muted.Render(help))
}

func (m model) View() string {
	if !m.ready {
		return "\n  Initializing..."
	}
	if m.err != nil {
		errorMsg := m.styles.Error.Render(fmt.Sprintf("Error: %v", m.err))
		help := m.styles.Muted.Render("\nIs the Kubernetes Metrics Server installed and running?")
		return m.styles.Base.Render(lipgloss.JoinVertical(lipgloss.Left, errorMsg, help))
	}

	var finalView string
	if m.view == viewLogs || m.view == viewHostLogs {
		finalView = fmt.Sprintf("%s\n%s\n%s", m.headerView(), m.viewport.View(), m.footerView())
	} else if m.view == viewYAML { // New case for YAML view
		m.viewport.SetContent(m.yamlContent)
		finalView = fmt.Sprintf("%s\n%s\n%s", m.headerView(), m.viewport.View(), m.footerView())
	} else if m.view == viewEdit {
		var b strings.Builder
		b.WriteString(m.editor.View())
		viewContent := b.String()
		finalView = lipgloss.JoinVertical(lipgloss.Left, m.headerView(), viewContent, m.footerView())
	} else if m.view == viewScaling {
		var b strings.Builder
		b.WriteString(m.details)
		b.WriteString("\n\nScale replicas: " + m.textInput.View())
		viewContent := b.String()
		finalView = lipgloss.JoinVertical(lipgloss.Left, m.headerView(), viewContent, m.footerView())
	} else if m.view == viewConfirmDelete {
		var b strings.Builder
		b.WriteString(m.details)
		b.WriteString(fmt.Sprintf("\n\nAre you sure you want to delete this pod? (y/n)"))
		viewContent := b.String()
		finalView = lipgloss.JoinVertical(lipgloss.Left, m.headerView(), viewContent, m.footerView())
	} else {
		var viewContent string
		switch m.view {
		case viewDetails:
			viewContent = m.details
		case viewYAML:
			m.viewport.SetContent(m.yamlContent)
			viewContent = m.viewport.View()
		case viewPods:
			viewContent = m.renderPodsList()
		case viewPVCs:
			viewContent = m.renderPVCsList()
		case viewPVs:
			viewContent = m.renderPVsList()
		case viewDeployments:
			viewContent = m.renderDeploymentsList()
		case viewStatefulSets:
			viewContent = m.renderStatefulSetsList()
		case viewDaemonSets:
			viewContent = m.renderDaemonSetsList()
		case viewServices:
			viewContent = m.renderServicesList()
		case viewNetworkPolicies:
			viewContent = m.renderNetworkPoliciesList()
		case viewEvents:
			viewContent = m.renderEventsList()
		case viewAlerts:
			viewContent = m.renderAlertsList()
		case viewResourceQuotas:
			viewContent = m.renderResourceQuotasList()
		case viewNamespaces:
			viewContent = m.renderNamespacesList()
		case viewResourceMenu:
			viewContent = m.renderResourceMenu()
		case viewWorkloads:
			viewContent = m.renderWorkloadsMenu()
		case viewStorage:
			viewContent = m.renderStorageMenu()
		case viewNetworkMenu:
			viewContent = m.renderNetworkMenu()
		case viewCluster:
			viewContent = m.renderClusterMenu()
		case viewSMTPConfig:
			viewContent = m.renderSMTPConfig()
		case viewHelp:
			viewContent = m.renderHelpView()
		case viewDashboard: // New case
			viewContent = m.renderDashboard()
		case viewHost:
			viewContent = m.renderHostView()
		case viewHostLogsMenu:
			viewContent = m.renderHostLogsMenu()
		default: // viewNodes
			viewContent = m.renderNodesList()
		}
		finalView = lipgloss.JoinVertical(lipgloss.Left, m.headerView(), viewContent, m.footerView())
	}

	return m.styles.Base.Render(finalView)
}

func (m *model) renderHostLogsMenu() string {
	var b strings.Builder
	b.WriteString(m.styles.HeaderText.Render("Select Log Type") + "\n")

	for i, logType := range m.hostLogTypes {
		style := m.styles.Row
		if m.cursor == i {
			style = m.styles.SelectedRow
		}
		b.WriteString(style.Render(logType) + "\n")
	}
	return b.String()
}

func (m *model) renderHelpView() string {
	var b strings.Builder
	b.WriteString(m.styles.HeaderText.Render("Keybindings") + "\n\n")
	b.WriteString("  Global:\n")
	b.WriteString("    q, ctrl+c: Quit\n")
	b.WriteString("    ?: Show this help view\n")
	b.WriteString("    r: Open resource selection menu\n")
	b.WriteString("    D: Show cluster dashboard\n")
	b.WriteString("    N: Select namespace\n\n")
	b.WriteString("  Navigation:\n")
	b.WriteString("    up/down: Move cursor\n")
	b.WriteString("    enter: Select / View details\n")
	b.WriteString("    esc: Go back\n\n")
	b.WriteString("  Details View (Pods):\n")
	b.WriteString("    l: View logs\n")
	b.WriteString("    d: Delete pod\n")
	b.WriteString("    y: View YAML\n\n")
	b.WriteString("  Details View (Deployments):\n")
	b.WriteString("    r: Scale replicas\n")
	b.WriteString("    e: Edit deployment\n")
	b.WriteString("    y: View YAML\n\n")
	b.WriteString("  Details View (Resource Quotas):\n")
	b.WriteString("    e: Edit resource quota\n")
	b.WriteString("    y: View YAML\n\n")
	b.WriteString("  Edit View:\n")
	b.WriteString("    ctrl+s: Save changes\n")
	b.WriteString("    esc: Cancel\n\n")
	b.WriteString("  Alerts View:\n")
	b.WriteString("    c: Configure SMTP\n")
	b.WriteString("    s: Send email\n\n")
	b.WriteString("  Other Details Views:\n")
	b.WriteString("    y: View YAML\n")
	return b.String()
}

func (m *model) renderWorkloadsMenu() string {
	var b strings.Builder
	b.WriteString(m.styles.HeaderText.Render("Select Workload") + "\n")

	for i, resourceType := range m.workloadTypes {
		style := m.styles.Row
		if m.cursor == i {
			style = m.styles.SelectedRow
		}
		b.WriteString(style.Render(resourceType) + "\n")
	}
	return b.String()
}

func (m *model) renderStorageMenu() string {
	var b strings.Builder
	b.WriteString(m.styles.HeaderText.Render("Select Storage Resource") + "\n")

	for i, resourceType := range m.storageTypes {
		style := m.styles.Row
		if m.cursor == i {
			style = m.styles.SelectedRow
		}
		b.WriteString(style.Render(resourceType) + "\n")
	}
	return b.String()
}

func (m *model) renderNetworkMenu() string {
	var b strings.Builder
	b.WriteString(m.styles.HeaderText.Render("Select Network Resource") + "\n")

	for i, resourceType := range m.networkTypes {
		style := m.styles.Row
		if m.cursor == i {
			style = m.styles.SelectedRow
		}
		b.WriteString(style.Render(resourceType) + "\n")
	}
	return b.String()
}

func (m *model) renderClusterMenu() string {
	var b strings.Builder
	b.WriteString(m.styles.HeaderText.Render("Select Cluster Resource") + "\n")

	for i, resourceType := range m.clusterTypes {
		style := m.styles.Row
		if m.cursor == i {
			style = m.styles.SelectedRow
		}
		b.WriteString(style.Render(resourceType) + "\n")
	}
	return b.String()
}

func (m *model) formatResourceQuotaDetails(rq v1.ResourceQuota) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Name:\t\t%s\n", rq.Name))
	b.WriteString(fmt.Sprintf("Namespace:\t%s\n", rq.Namespace))
	b.WriteString("\n" + m.styles.HeaderText.Render("Hard Limits") + "\n")
	for resourceName, limit := range rq.Spec.Hard {
		b.WriteString(fmt.Sprintf("  - %s:\t%s\n", resourceName, limit.String()))
	}
	b.WriteString("\n" + m.styles.HeaderText.Render("Used") + "\n")
	for resourceName, used := range rq.Status.Used {
		b.WriteString(fmt.Sprintf("  - %s:\t%s\n", resourceName, used.String()))
	}
	return b.String()
}

func (m *model) renderResourceQuotasList() string {
	var b strings.Builder
	if len(m.resourceQuotaLines) == 0 {
		return "No Resource Quotas found."
	}

	header := m.styles.Header.Render(fmt.Sprintf("%-40s %-30s %-20s %-20s", "NAME", "RESOURCE", "USED", "LIMIT"))
	b.WriteString(header + "\n")

	for i, lineData := range m.resourceQuotaLines {
		style := m.styles.Row
		if m.cursor == i {
			style = m.styles.SelectedRow
		}

		line := fmt.Sprintf("%-40s %-30s %-20s %-20s", lineData.Name, lineData.Resource, lineData.Used, lineData.Limit)
		b.WriteString(style.Render(line) + "\n")
	}
	return b.String()
}

func (m *model) renderAlertsList() string {
	var b strings.Builder
	if len(m.alerts) == 0 {
		return "No Alerts found."
	}

	header := m.styles.Header.Render(fmt.Sprintf("%-15s %-10s %-20s %-30s %s", "LAST SEEN", "TYPE", "REASON", "OBJECT", "MESSAGE"))
	b.WriteString(header + "\n")

	for i, a := range m.alerts {
		style := m.styles.Row
		if m.cursor == i {
			style = m.styles.SelectedRow
		}

		ts := a.LastTimestamp.Time.Format("15:04:05")
		obj := fmt.Sprintf("%s/%s", a.InvolvedObject.Kind, a.InvolvedObject.Name)
		msg := strings.Split(a.Message, "\n")[0] // First line only

		typeStyle := m.styles.Warning

		line := fmt.Sprintf("%-15s %-10s %-20s %-30s %s", ts, typeStyle.Render(a.Type), a.Reason, obj, msg)
		b.WriteString(style.Render(line) + "\n")
	}
	return b.String()
}

func (m *model) renderResourceMenu() string {
	var b strings.Builder
	b.WriteString(m.styles.HeaderText.Render("Select Resource Type") + "\n")

	for i, resourceType := range m.resourceTypes {
		style := m.styles.Row
		if m.cursor == i {
			style = m.styles.SelectedRow
		}
		b.WriteString(style.Render(resourceType) + "\n")
	}
	return b.String()
}

func (m *model) renderSMTPConfig() string {
	var b strings.Builder
	b.WriteString("SMTP Server: " + m.smtpServer.View() + "\n")
	b.WriteString("SMTP Port: " + m.smtpPort.View() + "\n")
	b.WriteString("Username: " + m.smtpUsername.View() + "\n")
	b.WriteString("Password: " + m.smtpPassword.View() + "\n")
	b.WriteString("Recipient Email: " + m.recipientEmail.View() + "\n")
	return b.String()
}

func (m *model) renderNamespacesList() string {
	var b strings.Builder

	// "All Namespaces" option
	style := m.styles.Row
	if m.cursor == 0 {
		style = m.styles.SelectedRow
	}
	b.WriteString(style.Render("[ All Namespaces ]") + "\n")

	for i, ns := range m.namespaces {
		style := m.styles.Row
		if m.cursor == i+1 {
			style = m.styles.SelectedRow
		}
		b.WriteString(style.Render(ns.Name) + "\n")
	}
	return b.String()
}

func (m *model) renderEventsList() string {
	var b strings.Builder
	if len(m.events) == 0 {
		return "No Events found."
	}

	header := m.styles.Header.Render(fmt.Sprintf("%-"+"15s %-"+"10s %-"+"20s %-"+"30s %s", "LAST SEEN", "TYPE", "REASON", "OBJECT", "MESSAGE"))
	b.WriteString(header + "\n")

	for i, e := range m.events {
		style := m.styles.Row
		if m.cursor == i {
			style = m.styles.SelectedRow
		}

		ts := e.LastTimestamp.Time.Format("15:04:05")
		obj := fmt.Sprintf("%s/%s", e.InvolvedObject.Kind, e.InvolvedObject.Name)
		msg := strings.Split(e.Message, "\n")[0] // First line only

		typeStyle := m.styles.Success
		if e.Type == "Warning" {
			typeStyle = m.styles.Warning
		}

		line := fmt.Sprintf("%-"+"15s %-"+"10s %-"+"20s %-"+"30s %s", ts, typeStyle.Render(e.Type), e.Reason, obj, msg)
		b.WriteString(style.Render(line) + "\n")
	}
	return b.String()
}

func (m *model) renderNetworkPoliciesList() string {
	var b strings.Builder
	if len(m.netpols) == 0 {
		return "No Network Policies found."
	}

	header := m.styles.Header.Render(fmt.Sprintf("%-"+"50s %s", "NAME", "POD SELECTOR"))
	b.WriteString(header + "\n")

	for i, p := range m.netpols {
		style := m.styles.Row
		if m.cursor == i {
			style = m.styles.SelectedRow
		}
		selector, _ := metav1.LabelSelectorAsSelector(&p.Spec.PodSelector)
		line := fmt.Sprintf("%-"+"50s %s", p.Name, selector.String())
		b.WriteString(style.Render(line) + "\n")
	}
	return b.String()
}

func (m *model) renderNodesList() string {
	var b strings.Builder
	if len(m.nodes) == 0 {
		return "Fetching nodes..."
	}

	header := m.styles.Header.Render(fmt.Sprintf("%-"+"40s %-"+"15s %-"+"10s %-"+"10s", "NAME", "STATUS", "CPU%", "MEM%"))
	b.WriteString(header + "\n")

	for i, node := range m.nodes {
		status := getNodeStatus(node)
		style := m.styles.Row
		if m.cursor == i {
			style = m.styles.SelectedRow
		}

		statusStyle := m.getStatusStyle(status)

		metrics, hasMetrics := m.nodeMetrics[node.Name]
		cpuPercent := "---"
		memPercent := "---"
		if hasMetrics {
			cpuPercent = formatPercentage(metrics.Usage.Cpu().MilliValue(), node.Status.Capacity.Cpu().MilliValue()) + "%"
			memPercent = formatPercentage(metrics.Usage.Memory().Value(), node.Status.Capacity.Memory().Value()) + "%"
		}
		line := fmt.Sprintf("%-"+"40s %-"+"15s %-"+"10s %-"+"10s", node.Name, statusStyle.Render(status), cpuPercent, memPercent)
		b.WriteString(style.Render(line) + "\n")
	}
	return b.String()
}

func (m *model) renderPodsList() string {
	var b strings.Builder
	if len(m.pods) == 0 {
		return "No Pods found."
	}

	header := m.styles.Header.Render(fmt.Sprintf("%-"+"40s %-"+"15s %-"+"10s %-"+"10s", "NAME", "STATUS", "CPU%", "MEM%"))
	b.WriteString(header + "\n")

	for i, pod := range m.pods {
		status := string(pod.Status.Phase)
		style := m.styles.Row
		if m.cursor == i {
			style = m.styles.SelectedRow
		}

		statusStyle := m.getStatusStyle(status)

		cpuPercent := "---"
		memPercent := "---"
		metrics, hasMetrics := m.podMetrics[pod.Name]
		if hasMetrics {
			cpuRequests := totalPodCPURequests(pod)
			memRequests := totalPodMemoryRequests(pod)
			cpuUsage := totalPodCPU(metrics)
			memUsage := totalPodMemory(metrics)

			if cpuRequests.MilliValue() > 0 {
				cpuPercent = formatPercentage(cpuUsage.MilliValue(), cpuRequests.MilliValue()) + "%"
			}
			if memRequests.Value() > 0 {
				memPercent = formatPercentage(memUsage.Value(), memRequests.Value()) + "%"
			}
		}
		line := fmt.Sprintf("%-"+"40s %-"+"15s %-"+"10s %-"+"10s", pod.Name, statusStyle.Render(status), cpuPercent, memPercent)
		b.WriteString(style.Render(line) + "\n")
	}
	return b.String()
}

func (m *model) renderPVCsList() string {
	var b strings.Builder
	if len(m.pvcs) == 0 {
		return "No PVCs found."
	}

	header := m.styles.Header.Render(fmt.Sprintf("%-"+"40s %-"+"15s %-"+"10s %s", "NAME", "STATUS", "CAPACITY", "VOLUME"))
	b.WriteString(header + "\n")

	for i, pvc := range m.pvcs {
		status := string(pvc.Status.Phase)
		style := m.styles.Row
		if m.cursor == i {
			style = m.styles.SelectedRow
		}
		statusStyle := m.getStatusStyle(status)
		capacity := pvc.Status.Capacity[v1.ResourceStorage]
		line := fmt.Sprintf("%-"+"40s %-"+"15s %-"+"10s %s", pvc.Name, statusStyle.Render(status), capacity.String(), pvc.Spec.VolumeName)
		b.WriteString(style.Render(line) + "\n")
	}
	return b.String()
}

func (m *model) renderPVsList() string {
	var b strings.Builder
	if len(m.pvs) == 0 {
		return "No PVs found."
	}

	header := m.styles.Header.Render(fmt.Sprintf("%-"+"40s %-"+"15s %-"+"10s %s", "NAME", "STATUS", "CAPACITY", "CLAIM"))
	b.WriteString(header + "\n")

	for i, pv := range m.pvs {
		status := string(pv.Status.Phase)
		style := m.styles.Row
		if m.cursor == i {
			style = m.styles.SelectedRow
		}
		statusStyle := m.getStatusStyle(status)
		capacity := pv.Spec.Capacity[v1.ResourceStorage]
		claim := ""
		if pv.Spec.ClaimRef != nil {
			claim = pv.Spec.ClaimRef.Name
		}
		line := fmt.Sprintf("%-"+"40s %-"+"15s %-"+"10s %s", pv.Name, statusStyle.Render(status), capacity.String(), claim)
		b.WriteString(style.Render(line) + "\n")
	}
	return b.String()
}

func (m *model) renderDeploymentsList() string {
	var b strings.Builder
	if len(m.deployments) == 0 {
		return "No Deployments found."
	}

	header := m.styles.Header.Render(fmt.Sprintf("%-"+"40s %-"+"10s", "NAME", "REPLICAS"))
	b.WriteString(header + "\n")

	for i, d := range m.deployments {
		style := m.styles.Row
		if m.cursor == i {
			style = m.styles.SelectedRow
		}
		replicas := fmt.Sprintf("%d/%d", d.Status.ReadyReplicas, d.Status.Replicas)
		line := fmt.Sprintf("%-"+"40s %-"+"10s", d.Name, replicas)
		b.WriteString(style.Render(line) + "\n")
	}
	return b.String()
}

func (m *model) renderStatefulSetsList() string {
	var b strings.Builder
	if len(m.statefulsets) == 0 {
		return "No StatefulSets found."
	}

	header := m.styles.Header.Render(fmt.Sprintf("%-"+"40s %-"+"10s", "NAME", "REPLICAS"))
	b.WriteString(header + "\n")

	for i, s := range m.statefulsets {
		style := m.styles.Row
		if m.cursor == i {
			style = m.styles.SelectedRow
		}
		replicas := fmt.Sprintf("%d/%d", s.Status.ReadyReplicas, s.Status.Replicas)
		line := fmt.Sprintf("%-"+"40s %-"+"10s", s.Name, replicas)
		b.WriteString(style.Render(line) + "\n")
	}
	return b.String()
}

func (m *model) renderDaemonSetsList() string {
	var b strings.Builder
	if len(m.daemonsets) == 0 {
		return "No DaemonSets found."
	}

	header := m.styles.Header.Render(fmt.Sprintf("%-"+"40s %-"+"10s", "NAME", "DESIRED/CURRENT"))
	b.WriteString(header + "\n")

	for i, d := range m.daemonsets {
		style := m.styles.Row
		if m.cursor == i {
			style = m.styles.SelectedRow
		}
		replicas := fmt.Sprintf("%d/%d", d.Status.DesiredNumberScheduled, d.Status.CurrentNumberScheduled)
		line := fmt.Sprintf("%-"+"40s %-"+"10s", d.Name, replicas)
		b.WriteString(style.Render(line) + "\n")
	}
	return b.String()
}

func (m *model) renderServicesList() string {
	var b strings.Builder
	if len(m.services) == 0 {
		return "No Services found."
	}

	header := m.styles.Header.Render(fmt.Sprintf("%-"+"40s %-"+"15s %-"+"15s %s", "NAME", "TYPE", "CLUSTER-IP", "PORTS"))
	b.WriteString(header + "\n")

	for i, s := range m.services {
		style := m.styles.Row
		if m.cursor == i {
			style = m.styles.SelectedRow
		}
		var ports []string
		for _, p := range s.Spec.Ports {
			ports = append(ports, fmt.Sprintf("%d:%d", p.Port, p.NodePort))
		}
		line := fmt.Sprintf("%-"+"40s %-"+"15s %-"+"15s %s", s.Name, s.Spec.Type, s.Spec.ClusterIP, strings.Join(ports, ","))
		b.WriteString(style.Render(line) + "\n")
	}
	return b.String()
}

func (m *model) renderHostView() string {
	var b strings.Builder
	b.WriteString(m.styles.HeaderText.Render("Host Resource Usage") + "\n")
	b.WriteString(fmt.Sprintf("  CPU:\t%s\n", m.hostCPUUsage))
	b.WriteString(fmt.Sprintf("  Memory:\t%s\n", m.hostMemoryUsage))
	b.WriteString("\n" + m.styles.HeaderText.Render("Disk Usage") + "\n")

	if len(m.hostDiskUsage) == 0 {
		b.WriteString("  No disk partitions found.\n")
	} else {
		header := m.styles.Header.Render(fmt.Sprintf("%-25s %-15s %-15s %-15s %-10s", "Mountpoint", "Total (GB)", "Used (GB)", "Free (GB)", "Used %"))
		b.WriteString(header + "\n")
		for _, d := range m.hostDiskUsage {
			totalGB := float64(d.Total) / (1024 * 1024 * 1024)
			usedGB := float64(d.Used) / (1024 * 1024 * 1024)
			freeGB := float64(d.Free) / (1024 * 1024 * 1024)
			line := fmt.Sprintf("%-25s %-15.2f %-15.2f %-15.2f %-10.2f%%", d.Mountpoint, totalGB, usedGB, freeGB, d.UsedPercent)
			b.WriteString(m.styles.Row.Render(line) + "\n")
		}
	}

	return b.String()
}

func (m *model) renderDashboard() string {
	var b strings.Builder

	b.WriteString(m.styles.HeaderText.Render("Cluster-wide Resource Usage") + "\n")
	b.WriteString(fmt.Sprintf("  CPU: %s\n", m.clusterCPUUsage))
	b.WriteString(fmt.Sprintf("  Memory: %s\n", m.clusterMemoryUsage))
	b.WriteString("\n")

	if len(m.topPodsByCPU) == 0 && len(m.topPodsByMemory) == 0 && len(m.topNodesByCPU) == 0 && len(m.topNodesByMemory) == 0 {
		b.WriteString("Metrics data not available. Ensure the metrics-server is installed and running.")
	} else {
		b.WriteString(m.styles.HeaderText.Render("Top Pods by CPU (mCores)") + "\n")
		b.WriteString(m.podCPUChart.View())
		b.WriteString("\n")
		b.WriteString(m.styles.HeaderText.Render("Top Pods by Memory (MiB)") + "\n")
		b.WriteString(m.podMemoryChart.View())
		b.WriteString("\n")
		b.WriteString(m.styles.HeaderText.Render("Top Nodes by CPU (mCores)") + "\n")
		b.WriteString(m.nodeCPUChart.View())
		b.WriteString("\n")
		b.WriteString(m.styles.HeaderText.Render("Top Nodes by Memory (MiB)") + "\n")
		b.WriteString(m.nodeMemoryChart.View())
	}

	return b.String()
}

func (m *model) formatEventDetails(e v1.Event) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Message:\t%s\n", e.Message))
	b.WriteString(fmt.Sprintf("Source:\t\t%s, %s\n", e.Source.Component, e.Source.Host))
	b.WriteString(fmt.Sprintf("Object:\t\t%s/%s\n", e.InvolvedObject.Kind, e.InvolvedObject.Name))
	b.WriteString(fmt.Sprintf("Count:\t\t%d\n", e.Count))
	b.WriteString(fmt.Sprintf("First Seen:\t%s\n", e.FirstTimestamp.Time.Format(time.RFC1123)))
	b.WriteString(fmt.Sprintf("Last Seen:\t%s\n", e.LastTimestamp.Time.Format(time.RFC1123)))
	return b.String()
}

func (m *model) formatNodeDetails(node v1.Node, metrics v1beta1.NodeMetrics, hasMetrics bool) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Name:\t%s\n", node.Name))
	b.WriteString(fmt.Sprintf("Status:\t%s\n", m.getStatusStyle(getNodeStatus(node)).Render(getNodeStatus(node))))
	b.WriteString(fmt.Sprintf("Roles:\t%s\n", getNodeRoles(node)))
	b.WriteString(fmt.Sprintf("Creation Timestamp:\t%s\n", node.CreationTimestamp.Format(time.RFC1123)))

	if hasMetrics {
		b.WriteString("\n" + m.styles.HeaderText.Render("Resource Usage") + "\n")
		b.WriteString(fmt.Sprintf("  CPU:\t%s / %s (%s%%)\n",
			formatMilliCPU(metrics.Usage.Cpu()),
			formatMilliCPU(node.Status.Capacity.Cpu()),
			formatPercentage(metrics.Usage.Cpu().MilliValue(), node.Status.Capacity.Cpu().MilliValue())))
		b.WriteString(fmt.Sprintf("  Memory:\t%s / %s (%s%%)\n",
			formatMiBMemory(metrics.Usage.Memory()),
			formatMiBMemory(node.Status.Capacity.Memory()),
			formatPercentage(metrics.Usage.Memory().Value(), node.Status.Capacity.Memory().Value())))
	}

	b.WriteString("\n" + m.styles.HeaderText.Render("System Info") + "\n")
	b.WriteString(fmt.Sprintf("  Architecture:\t%s\n", node.Status.NodeInfo.Architecture))
	b.WriteString(fmt.Sprintf("  OS:\t%s\n", node.Status.NodeInfo.OperatingSystem))
	b.WriteString(fmt.Sprintf("  OS Image:\t%s\n", node.Status.NodeInfo.OSImage))
	b.WriteString(fmt.Sprintf("  Kernel Version:\t%s\n", node.Status.NodeInfo.KernelVersion))
	b.WriteString(fmt.Sprintf("  Kubelet Version:\t%s\n", node.Status.NodeInfo.KubeletVersion))
	b.WriteString(fmt.Sprintf("  Container Runtime:\t%s\n", node.Status.NodeInfo.ContainerRuntimeVersion))

	return b.String()
}

func (m *model) formatPodDetails(pod v1.Pod, metrics v1beta1.PodMetrics, hasMetrics bool) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Name:\t%s\n", pod.Name))
	b.WriteString(fmt.Sprintf("Namespace:\t%s\n", pod.Namespace))
	b.WriteString(fmt.Sprintf("Status:\t%s\n", m.getStatusStyle(string(pod.Status.Phase)).Render(string(pod.Status.Phase))))
	b.WriteString(fmt.Sprintf("Pod IP:\t%s\n", pod.Status.PodIP))
	b.WriteString(fmt.Sprintf("Node:\t%s\n", pod.Spec.NodeName))
	b.WriteString(fmt.Sprintf("Creation Timestamp:\t%s\n", pod.CreationTimestamp.Format(time.RFC1123)))

	if hasMetrics {
		b.WriteString("\n" + m.styles.HeaderText.Render("Resource Usage") + "\n")
		cpuRequests := totalPodCPURequests(pod)
		memRequests := totalPodMemoryRequests(pod)
		cpuLimits := totalPodCPULimits(pod)
		memLimits := totalPodMemoryLimits(pod)

		cpuUsage := totalPodCPU(metrics)
		memUsage := totalPodMemory(metrics)

		cpuReqPercent := "---"
		if cpuRequests.MilliValue() > 0 {
			cpuReqPercent = formatPercentage(cpuUsage.MilliValue(), cpuRequests.MilliValue()) + "%"
		}
		memReqPercent := "---"
		if memRequests.Value() > 0 {
			memReqPercent = formatPercentage(memUsage.Value(), memRequests.Value()) + "%"
		}

		cpuLimPercent := "---"
		if cpuLimits.MilliValue() > 0 {
			cpuLimPercent = formatPercentage(cpuUsage.MilliValue(), cpuLimits.MilliValue()) + "%"
		}
		memLimPercent := "---"
		if memLimits.Value() > 0 {
			memLimPercent = formatPercentage(memUsage.Value(), memLimits.Value()) + "%"
		}

		b.WriteString(fmt.Sprintf("  CPU Usage:\t%s (Requests: %s, Limits: %s)\n",
			formatMilliCPU(cpuUsage), cpuReqPercent, cpuLimPercent))
		b.WriteString(fmt.Sprintf("  Memory Usage:\t%s (Requests: %s, Limits: %s)\n",
			formatMiBMemory(memUsage), memReqPercent, memLimPercent))
	}

	b.WriteString("\n" + m.styles.HeaderText.Render("Containers") + "\n")
	for _, c := range pod.Spec.Containers {
		readyStyle := m.styles.Muted
		if getContainerStatus(pod, c.Name) {
			readyStyle = m.styles.Success
		}
		b.WriteString(fmt.Sprintf("  - Name:\t%s\n", c.Name))
		b.WriteString(fmt.Sprintf("    Image:\t%s\n", c.Image))
		b.WriteString(fmt.Sprintf("    Ready:\t%s\n", readyStyle.Render(fmt.Sprintf("%t", getContainerStatus(pod, c.Name)))))
	}

	return b.String()
}

func (m *model) formatPVCDetails(pvc v1.PersistentVolumeClaim) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Name:\t\t%s\n", pvc.Name))
	b.WriteString(fmt.Sprintf("Namespace:\t%s\n", pvc.Namespace))
	b.WriteString(fmt.Sprintf("Status:\t\t%s\n", m.getStatusStyle(string(pvc.Status.Phase)).Render(string(pvc.Status.Phase))))
	b.WriteString(fmt.Sprintf("Volume:\t\t%s\n", pvc.Spec.VolumeName))

	storageClassName := "<none>"
	if pvc.Spec.StorageClassName != nil {
		storageClassName = *pvc.Spec.StorageClassName
	}
	b.WriteString(fmt.Sprintf("StorageClass:\t%s\n", storageClassName))

	capacity := pvc.Status.Capacity[v1.ResourceStorage]
	b.WriteString(fmt.Sprintf("Capacity:\t%s\n", capacity.String()))

	b.WriteString("\n" + m.styles.HeaderText.Render("Access Modes") + "\n")
	for _, mode := range pvc.Spec.AccessModes {
		b.WriteString(fmt.Sprintf("  - %s\n", mode))
	}

	return b.String()
}

func (m *model) formatPVDetails(pv v1.PersistentVolume) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Name:\t\t%s\n", pv.Name))
	b.WriteString(fmt.Sprintf("Status:\t\t%s\n", m.getStatusStyle(string(pv.Status.Phase)).Render(string(pv.Status.Phase))))
	if pv.Spec.ClaimRef != nil {
		b.WriteString(fmt.Sprintf("Claim:\t\t%s/%s\n", pv.Spec.ClaimRef.Namespace, pv.Spec.ClaimRef.Name))
	}
	b.WriteString(fmt.Sprintf("Reclaim Policy:\t%s\n", pv.Spec.PersistentVolumeReclaimPolicy))

	storageClassName := "<none>"
	if pv.Spec.StorageClassName != "" {
		storageClassName = pv.Spec.StorageClassName
	}
	b.WriteString(fmt.Sprintf("StorageClass:\t%s\n", storageClassName))

	capacity := pv.Spec.Capacity[v1.ResourceStorage]
	b.WriteString(fmt.Sprintf("Capacity:\t%s\n", capacity.String()))

	b.WriteString("\n" + m.styles.HeaderText.Render("Access Modes") + "\n")
	for _, mode := range pv.Spec.AccessModes {
		b.WriteString(fmt.Sprintf("  - %s\n", mode))
	}

	return b.String()
}

func (m *model) formatDeploymentDetails(d appsv1.Deployment) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Name:\t\t%s\n", d.Name))
	b.WriteString(fmt.Sprintf("Namespace:\t%s\n", d.Namespace))
	b.WriteString(fmt.Sprintf("Replicas:\t%d desired | %d updated | %d total | %d available | %d unavailable\n",
		*d.Spec.Replicas, d.Status.UpdatedReplicas, d.Status.Replicas, d.Status.AvailableReplicas, d.Status.UnavailableReplicas))
	b.WriteString(fmt.Sprintf("Strategy:\t%s\n", d.Spec.Strategy.Type))

	return b.String()
}

func (m *model) formatStatefulSetDetails(s appsv1.StatefulSet) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Name:\t\t%s\n", s.Name))
	b.WriteString(fmt.Sprintf("Namespace:\t%s\n", s.Namespace))
	b.WriteString(fmt.Sprintf("Replicas:\t%d desired | %d ready\n",
		*s.Spec.Replicas, s.Status.ReadyReplicas))
	b.WriteString(fmt.Sprintf("Service Name:\t%s\n", s.Spec.ServiceName))

	return b.String()
}

func (m *model) formatDaemonSetDetails(d appsv1.DaemonSet) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Name:\t\t%s\n", d.Name))
	b.WriteString(fmt.Sprintf("Namespace:\t%s\n", d.Namespace))
	b.WriteString(fmt.Sprintf("Pods:\t%d desired | %d current | %d ready\n",
		d.Status.DesiredNumberScheduled, d.Status.CurrentNumberScheduled, d.Status.NumberReady))

	return b.String()
}

func (m *model) formatServiceDetails(s v1.Service) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Name:\t\t%s\n", s.Name))
	b.WriteString(fmt.Sprintf("Namespace:\t%s\n", s.Namespace))
	b.WriteString(fmt.Sprintf("Type:\t\t%s\n", s.Spec.Type))
	b.WriteString(fmt.Sprintf("Cluster IP:\t%s\n", s.Spec.ClusterIP))
	b.WriteString(fmt.Sprintf("External IP:\t%s\n", s.Spec.LoadBalancerIP))

	b.WriteString("\n" + m.styles.HeaderText.Render("Ports") + "\n")
	for _, p := range s.Spec.Ports {
		b.WriteString(fmt.Sprintf("  - %s:%d -> %d/%s\n", p.Name, p.Port, p.NodePort, p.Protocol))
	}

	return b.String()
}

func (m *model) formatNetworkPolicyDetails(p networkingv1.NetworkPolicy) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Name:\t\t%s\n", p.Name))
	b.WriteString(fmt.Sprintf("Namespace:\t%s\n", p.Namespace))

	selector, _ := metav1.LabelSelectorAsSelector(&p.Spec.PodSelector)
	b.WriteString(fmt.Sprintf("Pod Selector:\t%s\n", selector.String()))

	b.WriteString("\n" + m.styles.HeaderText.Render("Policy Types") + "\n")
	for _, pt := range p.Spec.PolicyTypes {
		b.WriteString(fmt.Sprintf("  - %s\n", pt))
	}

	b.WriteString("\n" + m.styles.HeaderText.Render("Ingress Rules") + "\n")
	if len(p.Spec.Ingress) == 0 {
		b.WriteString("  (none)\n")
	}
	for _, i := range p.Spec.Ingress {
		b.WriteString("  - Ports:\n")
		for _, p := range i.Ports {
			b.WriteString(fmt.Sprintf("    - %s:%s\n", *p.Protocol, p.Port.String()))
		}
		b.WriteString("    From:\n")
		for _, f := range i.From {
			if f.PodSelector != nil {
				ps, _ := metav1.LabelSelectorAsSelector(f.PodSelector)
				b.WriteString(fmt.Sprintf("      - PodSelector: %s\n", ps.String()))
			}
			if f.NamespaceSelector != nil {
				ns, _ := metav1.LabelSelectorAsSelector(f.NamespaceSelector)
				b.WriteString(fmt.Sprintf("      - NamespaceSelector: %s\n", ns.String()))
			}
		}
	}

	b.WriteString("\n" + m.styles.HeaderText.Render("Egress Rules") + "\n")
	if len(p.Spec.Egress) == 0 {
		b.WriteString("  (none)\n")
	}
	for _, e := range p.Spec.Egress {
		b.WriteString("  - Ports:\n")
		for _, p := range e.Ports {
			b.WriteString(fmt.Sprintf("    - %s:%s\n", *p.Protocol, p.Port.String()))
		}
		b.WriteString("    To:\n")
		for _, t := range e.To {
			if t.PodSelector != nil {
				ps, _ := metav1.LabelSelectorAsSelector(t.PodSelector)
				b.WriteString(fmt.Sprintf("      - PodSelector: %s\n", ps.String()))
			}
			if t.NamespaceSelector != nil {
				ns, _ := metav1.LabelSelectorAsSelector(t.NamespaceSelector)
				b.WriteString(fmt.Sprintf("      - NamespaceSelector: %s\n", ns.String()))
			}
		}
	}

	return b.String()
}

func (m *model) getStatusStyle(status string) lipgloss.Style {
	switch strings.ToLower(status) {
	case "running", "bound", "ready", "available", "active":
		return m.styles.Success
	case "pending":
		return m.styles.Warning
	case "failed", "error", "notready", "terminated", "lost":
		return m.styles.Error
	default:
		return m.styles.Muted
	}
}

func getNodeStatus(node v1.Node) string {
	for _, c := range node.Status.Conditions {
		if c.Type == v1.NodeReady {
			if c.Status == v1.ConditionTrue {
				return "Ready"
			}
			return "NotReady"
		}
	}
	return "Unknown"
}

func getNodeRoles(node v1.Node) string {
	var roles []string
	for k := range node.Labels {
		if strings.HasPrefix(k, "node-role.kubernetes.io/") {
			roles = append(roles, strings.TrimPrefix(k, "node-role.kubernetes.io/"))
		}
	}
	if len(roles) == 0 {
		return "<none>"
	}
	return strings.Join(roles, ",")
}

func getContainerStatus(pod v1.Pod, containerName string) bool {
	for _, s := range pod.Status.ContainerStatuses {
		if s.Name == containerName {
			return s.Ready
		}
	}
	return false
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
		return "---"
	}
	return fmt.Sprintf("%dm", q.MilliValue())
}

func formatMiBMemory(q *resource.Quantity) string {
	if q == nil {
		return "---"
	}
	return fmt.Sprintf("%dMi", q.Value()/(1024*1024))
}

func formatPercentage(val, total int64) string {
	if total == 0 {
		return "0"
	}
	return fmt.Sprintf("%.0f", float64(val)*100/float64(total))
}

func totalPodCPURequests(pod v1.Pod) *resource.Quantity {
	total := resource.NewQuantity(0, resource.DecimalSI)
	for _, c := range pod.Spec.Containers {
		if c.Resources.Requests != nil {
			if cpu, ok := c.Resources.Requests[v1.ResourceCPU]; ok {
				total.Add(cpu)
			}
		}
	}
	return total
}

func totalPodMemoryRequests(pod v1.Pod) *resource.Quantity {
	total := resource.NewQuantity(0, resource.BinarySI)
	for _, c := range pod.Spec.Containers {
		if c.Resources.Requests != nil {
			if mem, ok := c.Resources.Requests[v1.ResourceMemory]; ok {
				total.Add(mem)
			}
		}
	}
	return total
}

func totalPodCPULimits(pod v1.Pod) *resource.Quantity {
	total := resource.NewQuantity(0, resource.DecimalSI)
	for _, c := range pod.Spec.Containers {
		if c.Resources.Limits != nil {
			if cpu, ok := c.Resources.Limits[v1.ResourceCPU]; ok {
				total.Add(cpu)
			}
		}
	}
	return total
}

func totalPodMemoryLimits(pod v1.Pod) *resource.Quantity {
	total := resource.NewQuantity(0, resource.BinarySI)
	for _, c := range pod.Spec.Containers {
		if c.Resources.Limits != nil {
			if mem, ok := c.Resources.Limits[v1.ResourceMemory]; ok {
				total.Add(mem)
			}
		}
	}
	return total
}

func main() {
	var kubeconfig string
	flag.StringVar(&kubeconfig, "kubeconfig", "", "path to the kubeconfig file")
	flag.Parse()

	if kubeconfig == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Printf("Error getting user home directory: %v\n", err)
			os.Exit(1)
		}
		kubeconfig = filepath.Join(home, ".kube", "config")
	}

	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		fmt.Printf("Error building kubeconfig: %v\n", err)
		os.Exit(1)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		fmt.Printf("Error creating clientset: %v\n", err)
		os.Exit(1)
	}

	metricsClientset, err := metrics.NewForConfig(config)
	if err != nil {
		fmt.Printf("Error creating metrics clientset: %v\n", err)
		os.Exit(1)
	}

	ti := textinput.New()
	ti.Placeholder = "3"
	ti.CharLimit = 3
	ti.Width = 5

	editor := textarea.New()
	editor.Placeholder = "Enter patch here"
	editor.SetWidth(80)
	editor.SetHeight(10)

	podCPUChart := barchart.New(10, 20)
	podMemoryChart := barchart.New(10, 20)
	nodeCPUChart := barchart.New(10, 20)
	nodeMemoryChart := barchart.New(10, 20)

	smtpPassword := textinput.New()
	smtpPassword.Placeholder = "Password"
	smtpPassword.EchoMode = textinput.EchoPassword
	smtpPassword.CharLimit = 64

	initialModel := model{
		clientset:        clientset,
		metricsClientset: metricsClientset,
		styles:           defaultStyles(),
		textInput:        ti,
		editor:           editor,
		podCPUChart:      podCPUChart,
		podMemoryChart:   podMemoryChart,
		nodeCPUChart:     nodeCPUChart,
		nodeMemoryChart:  nodeMemoryChart,
		resourceTypes:    []string{"Workloads", "Storage", "Network", "Cluster"},
		workloadTypes:    []string{"Pods", "Deployments", "StatefulSets", "DaemonSets"},
		storageTypes:     []string{"PVCs", "PVs"},
		networkTypes:     []string{"Services", "Network Policies"},
		clusterTypes:     []string{"Nodes", "Namespaces", "Events", "Alerts", "Resource Quotas"},
		hostLogTypes:     []string{"System Logs", "Kubelet Logs", "Docker Logs"},
		smtpServer:       textinput.New(),
		smtpPort:         textinput.New(),
		smtpUsername:     textinput.New(),
		smtpPassword:     smtpPassword,
		recipientEmail:   textinput.New(),
	}

	p := tea.NewProgram(initialModel, tea.WithAltScreen())
	if err := p.Start(); err != nil {
		fmt.Printf("Alas, there's been an error: %v", err)
		os.Exit(1)
	}
}
