## Experiment shell

This folder contains various shell scripts used to deploy and manage network performance testing methods for Kubernetes clusters. The main purpose of these scripts is to deploy and test bandwidth, latency, and performance between multiple pods and services within the cluster using tools like `iperf`, `nuttcp`, and `Prometheus`.

The scripts in this folder can be categorized into several key functions:

1. **iperf-server**: Used to deploy a specified number of `iperf-server` pods to receive `iperf` data packets.
2. **iperf-client**: Used to deploy the corresponding number of `iperf-client` pods to send `iperf` data packets and initiate bandwidth tests.
3. **nuttcp**: Uses the `nuttcp` tool to conduct bandwidth and latency tests across the cluster.
4. **create-service**: Creates different numbers of Kubernetes services that correspond to each `iperf` pod and tests cross-cluster service discovery latency.
5. **log**: Collects and aggregates the measurement results of all `iperf` tests. These results can also be monitored through Prometheus.
6. **link-recovery**: Measures the time it takes for a Pod's network link to recover after being interrupted, simulating real-world network failures and recovery scenarios.

### Detailed Description of Each Component

#### 1. **iperf-server**

This script is used to deploy a set number of `iperf-server` pods in the Kubernetes cluster. Each pod will listen for incoming data packets and perform bandwidth measurement based on the data received. The server-side pods act as the receivers, where the `iperf-client` pods send test traffic to measure bandwidth between pods or services in the cluster.

**Key Functions:**

- Deploys a configurable number of `iperf-server` pods.
- Exposes a port on each pod (usually 5201 by default) for listening.
- Each server pod will provide bandwidth statistics based on the traffic received.

#### 2. **iperf-client**

This script deploys the corresponding number of `iperf-client` pods in the cluster. These pods act as the clients in the network, sending traffic to the `iperf-server` pods and measuring the bandwidth and latency for each connection.

**Key Functions:**

- Deploys a configurable number of `iperf-client` pods.
- Connects to the `iperf-server` pods to start the test.
- Outputs bandwidth, jitter, and other network statistics for analysis.

#### 3. **nuttcp**

This part of the script utilizes `nuttcp`, a popular network performance testing tool, to conduct network bandwidth and latency tests across the Kubernetes cluster. Unlike `iperf`, `nuttcp` is often used for more specific networking needs, such as testing TCP/UDP throughput or identifying bottlenecks in network traffic.

**Key Functions:**

- Deploys and configures `nuttcp` clients and servers.
- Conducts bandwidth tests over both TCP and UDP protocols.
- Useful for detailed network performance analysis, such as measuring throughput under different load conditions.

#### 4. **create-service**

The `create-service` script is used to create a set of Kubernetes services that correspond to each `iperf-server` pod deployed. This allows you to test service discovery latency between different services across the Kubernetes cluster, simulating the real-world scenario of service-to-service communication.

**Key Functions:**

- Creates multiple Kubernetes services with unique names corresponding to each `iperf-server`.
- Tests the latency involved in service discovery, simulating the time it takes for clients to discover and connect to the correct services.
- Ensures that the service discovery mechanism works correctly in a multi-pod environment, with accurate DNS resolution and routing.

#### 5. **log**

This script is responsible for collecting the test results from all `iperf` client and server pods. It aggregates the data and provides a centralized report of the bandwidth performance, latency, and other statistics from all the tests conducted.

**Key Functions:**

- Collects logs from all `iperf`-client and `iperf`-server pods.
- Aggregates test results into a single log file for easy access and analysis.
- Supports integration with **Prometheus** to store and visualize the results in real time.
- Can be used to generate reports for historical analysis and performance benchmarking.

#### 6. **link-recovery**

In addition to bandwidth and latency tests, this script is designed to measure the link recovery time of a Pod after a network interruption. This simulates a network failure scenario, where the link to a Pod is intentionally severed and then restored. The script measures the time it takes for the Pod's link to recover and resume normal operation.

**Key Functions:**

- Simulates network link failure for a given Pod using `iptables` or other network isolation methods.
- Measures the time it takes for the network link to recover after the failure is cleared.
- Monitors and logs the recovery process by attempting to ping external services or other pods in the cluster to determine when connectivity is restored.
- Useful for evaluating Kubernetes cluster robustness and the time it takes to recover from network failures.

### Using Prometheus for Monitoring and Visualization

The **log** component also integrates with **Prometheus** for real-time monitoring. By exposing `iperf` and `nuttcp` results as metrics, Prometheus can scrape the data and store it for long-term analysis. The collected metrics can be visualized in **Grafana** dashboards for detailed insights into network performance.

**Key Prometheus Integration Features:**

- Metrics are pushed from `iperf` and `nuttcp` tests to Prometheus.
- Time series data is stored for long-term performance monitoring.
- Grafana dashboards can visualize the metrics, including throughput, packet loss, latency, jitter, and more.

This setup enables automated monitoring of network performance over time, ensuring that any issues with bandwidth, latency, or network configuration are easily identifiable.

