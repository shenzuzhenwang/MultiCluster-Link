## Submariner

This guide will walk you through deploying **Submariner** using Helm to enable connectivity between Kubernetes clusters.

#### Prerequisites

1. **Helm 3**: Ensure Helm 3 is installed.
2. **Kubernetes clusters**: At least two clusters must be set up and reachable.
3. **kubectl**: Ensure `kubectl` is installed and configured to access your clusters.

#### Step 1: Add the Submariner Helm Repository

First, add the Submariner Helm chart repository.

```bash
helm repo add submariner https://submariner.io/charts
helm repo update
```

**Explanation**: These commands add the Submariner chart repository to Helm and update your local chart index.

#### Step 2: Install Submariner in Each Cluster

##### Cluster A

```
helm install submariner submariner/submariner --namespace submariner --create-namespace
```

**Explanation**: Install Submariner in Cluster A by creating a namespace named `submariner` and deploying the Submariner chart.

##### Cluster B

```
helm install submariner submariner/submariner --namespace submariner --create-namespace
```

**Explanation**: Similarly, install Submariner in Cluster B. Make sure to apply the Helm chart to both clusters.

#### Step 3: Verify the Installation

Check the status of the Submariner components:

```
kubectl get pods -n submariner
```

**Explanation**: This command checks if the Submariner pods are running properly in both clusters.

#### Step 4: Join the Clusters

Generate a connection token in Cluster A:

```
kubectl -n submariner get secret submariner-join-token -o yaml
```

Copy the token output and use it in Cluster B to join the clusters.

```
kubectl create secret generic submariner-join-token --from-literal=token=<your-token> -n submariner
```

**Explanation**: This step generates a join token in Cluster A and applies it to Cluster B to establish the connection between the clusters.

#### Step 5: Verify Cross-Cluster Connectivity

Once the clusters are connected, you can verify the connectivity between them by pinging a pod in the other cluster:

```
kubectl exec -it <pod-in-cluster-a> -- ping <pod-in-cluster-b>
```

**Explanation**: Use `kubectl exec` to run a ping command inside a pod in Cluster A to verify connectivity to a pod in Cluster B.

#### Step 6: Clean Up

If you want to uninstall Submariner, run the following command in both clusters:

```
helm uninstall submariner --namespace submariner
```

**Explanation**: This command removes Submariner and all its associated resources from the `submariner` namespace.