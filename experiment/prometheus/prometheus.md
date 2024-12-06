### Prometheus

Prometheus is used as a monitoring tool for the experiment, responsible for monitoring each gateway pod and the entire k8s cluster network transmission.

#### Prerequisites

1. **Kubernetes Cluster**: A running Kubernetes cluster.
2. **kubectl**: Installed and configured to interact with your Kubernetes cluster.
3. **Helm**: Installed on your local machine.

---

##### Step 1: Add the Prometheus Helm Chart Repository

First, add the official **Prometheus** Helm repository and update your local chart information.

```bash
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts
helm repo update
```

**Explanation**: This command adds the Prometheus community Helm chart repository to Helm and updates your local chart index to fetch the latest available charts.

#### Step 2: Install Prometheus using Helm

You can install Prometheus with the default settings using the following Helm command:

```
helm install prometheus prometheus-community/kube-prometheus-stack --namespace monitoring --create-namespace
```

**Explanation**: This installs the `kube-prometheus-stack` chart into a `monitoring` namespace. The chart includes Prometheus, Alertmanager, and other monitoring components like Grafana.

------

#### Step 3: Verify Prometheus Installation

Once the Helm installation is complete, you can verify that Prometheus is running by checking the pods in the `monitoring` namespace:

```
kubectl get pods -n monitoring
```

**Explanation**: This command lists all pods in the `monitoring` namespace. Look for the Prometheus-related pods (`prometheus-operator`, `prometheus-server`, etc.).

------

#### Step 4: Expose Prometheus for External Access (Optional)

If you want to access the Prometheus UI from outside the Kubernetes cluster, you can expose the Prometheus server via a Kubernetes service. You can port-forward the Prometheus service to your local machine:

```
kubectl port-forward svc/prometheus-operated -n monitoring 9090:9090
```

**Explanation**: This command forwards port `9090` from the Prometheus service to your local machine, allowing you to access the Prometheus UI at `http://localhost:9090`.

Alternatively, you can create an Ingress or LoadBalancer service for more permanent external access.

#### Step 5: Access Prometheus Dashboard

Once the port forwarding is done, you can access the Prometheus UI by navigating to:

```
http://localhost:9090
```

**Explanation**: This opens the Prometheus web interface where you can query metrics, view dashboards, and monitor your cluster’s performance.

#### Step 6: Access Grafana Dashboard (Optional)

The `kube-prometheus-stack` also installs **Grafana** for visualizing Prometheus metrics. To access the Grafana dashboard, follow the steps below:

1. Port-forward the Grafana service:

```
kubectl port-forward svc/grafana -n monitoring 3000:3000
```

2. Open the Grafana UI at:

```
http://localhost:3000
```

**Explanation**: By default, the Grafana username is `admin` and the password is also `admin`. After logging in, you can set up Grafana to display various pre-built Prometheus dashboards.

#### Step 7: Monitor Kubernetes Cluster

Prometheus will automatically discover Kubernetes components and begin scraping metrics from them. You can start monitoring various resources, such as:

- Nodes
- Pods
- Deployments
- Services