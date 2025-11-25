# KubeView: A Terminal-Based Kubernetes Cluster Visualizer

KubeView is a command-line interface (CLI) tool written in Go that provides a real-time, interactive visualization of your Kubernetes (EKS) cluster directly in your terminal. It leverages the `client-go` library to interact with the Kubernetes API and `bubbletea` for a rich terminal user interface (TUI).

## Table of Contents

1.  [Project Overview](#project-overview)
2.  [Features](#features)
3.  [Screenshots](#screenshots)
4.  [Prerequisites](#prerequisites)
    *   [Go Programming Language](#go-programming-language)
    *   [K3s Kubernetes Distribution](#k3s-kubernetes-distribution)
    *   [Kubernetes Metrics Server](#kubernetes-metrics-server)
5.  [Installation Guide](#installation-guide)
    *   [Install Go](#install-go)
    *   [Install K3s](#install-k3s)
    *   [Install Kubernetes Metrics Server](#install-kubernetes-metrics-server)
    *   [Install KubeView Dependencies](#install-kubeview-dependencies)
    *   [Build KubeView](#build-kubeview)
6.  [Running KubeView](#running-kubeview)
7.  [Usage](#usage)
8.  [Project Structure](#project-structure)
9.  [Contributing](#contributing)
10. [License](#license)

## Project Overview

KubeView aims to simplify the monitoring and understanding of Kubernetes cluster resources by presenting them in an intuitive, interactive terminal interface. Instead of relying on complex `kubectl` commands or external dashboards, KubeView offers a quick and easy way to visualize the state of your pods, deployments, services, and other resources.

## Features

*   **Interactive Terminal UI:** Visualize your Kubernetes resources in a user-friendly, terminal-based interface.
*   **Real-time Updates:** Get real-time updates of your cluster's status.
*   **Categorized Resource Menu:** Navigate through a reorganized, categorized resource menu.
*   **Graphical Dashboard:** View a graphical dashboard with charts for resource utilization.
*   **Alerting System:** Monitor for potential issues with the built-in alerting system.
*   **Resource Quota Monitoring:** Keep track of your resource quotas and usage.
*   **Deployment Patching:** Patch your deployments directly from the TUI.
*   **Resource Quota Editing:** Edit your resource quotas on the fly.
*   **Lightweight and Efficient:** Built with Go for optimal performance.

## Screenshots

### Dashboard View

```
 Cluster Dashboard
 ────────────────────────────────────────────────────────────────────────────────────────────────
 Cluster-wide Resource Usage
   CPU: 100m / 1000m (10%)
   Memory: 500Mi / 2048Mi (24%)

 Top Pods by CPU Usage
 ┌──────────────────────────────────────────────────────────────────────────────────────────────┐
 │                                       Top Pods by CPU Usage                                  │
 │                                                                                              │
 │   nginx-deployment-5f5d8f6c8c-l5z8z   बारा बारा बारा बारा बारा बारा बारा बारा बारा बारा बारा  100m│
 │   coredns-5f5d8f6c8c-l5z8z          बारा बारा बारा बारा बारा बारा बारा                         50m│
 │   metrics-server-5f5d8f6c8c-l5z8z   बारा बारा बारा बारा                                        20m│
 └──────────────────────────────────────────────────────────────────────────────────────────────┘

 Top Pods by Memory Usage
 ┌──────────────────────────────────────────────────────────────────────────────────────────────┐
 │                                     Top Pods by Memory Usage                                 │
 │                                                                                              │
 │   nginx-deployment-5f5d8f6c8c-l5z8z बारा बारा बारा बारा बारा बारा बारा बारा बारा बारा बारा 200Mi│
 │   coredns-5f5d8f6c8c-l5z8z           बारा बारा बारा बारा बारा                                 100Mi│
 │   metrics-server-5f5d8f6c8c-l5z8z   बारा बारा                                                 50Mi│
 └──────────────────────────────────────────────────────────────────────────────────────────────┘
```

### Resource Menu

```
 Select Resource Category
 ────────────────────────────────────────────────────────────────────────────────────────────────
 > Workloads
   Storage
   Network
   Cluster
```

## Prerequisites

Before you can install and run KubeView, you need to have the following components installed on your system:

### Go Programming Language

KubeView is written in Go, so you need Go installed to build and run it.

### K3s Kubernetes Distribution

KubeView is designed to work with Kubernetes clusters, and for local development and testing, K3s is an excellent lightweight option.

### Kubernetes Metrics Server

To display resource utilization (CPU, Memory), KubeView relies on the Kubernetes Metrics Server being installed and running in your cluster.

## Installation Guide

Follow these steps to set up your environment and install KubeView.

### Install Go

If you don't have Go installed, follow these instructions. For the latest version, always refer to the [official Go documentation](https://golang.org/doc/install).

1.  **Download Go:**
    Visit the [Go downloads page](https://golang.org/dl/) and download the appropriate package for your system. For Linux, you'll typically download a `.tar.gz` file.

    ```bash
    # Example for Linux x64, replace with the latest version
    wget https://golang.org/dl/go1.21.5.linux-amd64.tar.gz
    ```

2.  **Extract the archive:**
    Extract the downloaded archive to `/usr/local`.

    ```bash
    sudo rm -rf /usr/local/go
    sudo tar -C /usr/local -xzf go1.21.5.linux-amd64.tar.gz
    ```

3.  **Set up environment variables:**
    Add Go to your `PATH` environment variable. You can do this by adding the following line to your `~/.profile` or `~/.bashrc` file:

    ```bash
    echo "export PATH=$PATH:/usr/local/go/bin" >> ~/.profile
    source ~/.profile
    ```
    Or for `~/.bashrc`:
    ```bash
    echo "export PATH=$PATH:/usr/local/go/bin" >> ~/.bashrc
    source ~/.bashrc
    ```

4.  **Verify installation:**
    ```bash
    go version
    ```
    You should see the installed Go version.

### Install K3s

K3s is a lightweight Kubernetes distribution.

1.  **Run the installation command:**
    Execute the following command in your terminal. This script will download and install K3s, configure systemd services, and set up a Kubeconfig file.

    ```bash
    curl -sfL https://get.k3s.io | sh -
    ```

2.  **Verify the installation:**
    After the installation completes, check the status of your K3s cluster:

    ```bash
    kubectl get nodes
    ```
    Your node should be in a "Ready" state.

3.  **Kubeconfig Location:**
    The Kubeconfig file for K3s is typically located at `/etc/rancher/k3s/k3s.yaml`. You will need this path when running KubeView.

### Install Kubernetes Metrics Server

The Metrics Server provides resource usage data for pods and nodes, which KubeView can display. K3s often includes the Metrics Server by default.

1.  **Verify if Metrics Server is running:**
    Check for the Metrics Server API service:

    ```bash
    kubectl get apiservices | grep metrics
    ```
    If you see `v1beta1.metrics.k8s.io` with a status of `Available`, it's already running.

2.  **Manual Installation (if not running):**
    If the Metrics Server is not running, you can install it manually:

    *   **Download the manifest:**
        ```bash
        curl -LO https://github.com/kubernetes-sigs/metrics-server/releases/latest/download/components.yaml
        ```

    *   **Apply the manifest:**
        ```bash
        kubectl apply -f components.yaml
        ```

    *   **Verify installation:**
        Wait a few moments, then check again:
        ```bash
        kubectl get apiservices | grep metrics
        kubectl get pods -n kube-system -l k8s-app=metrics-server
        kubectl top nodes
        kubectl top pods --all-namespaces
        ```

### Install KubeView Dependencies

Navigate to the `kubeview` project directory and install the Go modules.

```bash
cd /path/to/kubeview
go mod tidy
```

### Build KubeView

Build the KubeView executable:

```bash
cd /path/to/kubeview
go build -o kubeview .
```
This will create an executable named `kubeview` in your current directory.

## Running KubeView

To run KubeView, you need to specify the path to your Kubernetes Kubeconfig file using the `-kubeconfig` flag.

```bash
./kubeview -kubeconfig /etc/rancher/k3s/k3s.yaml
```
Replace `/etc/rancher/k3s/k3s.yaml` with the actual path to your Kubeconfig file if it's different.

## Usage

KubeView is an interactive TUI. Use the following keybindings to navigate the application:

*   **q, ctrl+c:** Quit
*   **?:** Show this help view
*   **r:** Open resource selection menu
*   **D:** Show cluster dashboard
*   **N:** Select namespace
*   **up/down:** Move cursor
*   **enter:** Select / View details
*   **esc:** Go back

### Details View (Pods)

*   **l:** View logs
*   **d:** Delete pod
*   **y:** View YAML

### Details View (Deployments)

*   **r:** Scale replicas
*   **e:** Edit deployment
*   **y:** View YAML

### Details View (Resource Quotas)

*   **e:** Edit resource quota
*   **y:** View YAML

## Project Structure

```
kubeview/
├── go.mod
├── go.sum
├── main.go
└── styles.go
```

*   `go.mod`: Go module definition file.
*   `go.sum`: Checksums for module dependencies.
*   `main.go`: The main application logic for KubeView.
*   `styles.go`: Defines the styling for the terminal UI.

## Contributing

Contributions are welcome! Please feel free to open issues or submit pull requests.

## License

This project is licensed under the MIT License. See the `LICENSE` file for details (to be added).
