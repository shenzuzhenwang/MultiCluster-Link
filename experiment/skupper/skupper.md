## Skupper

https://skupper.io/start/index.html

Here’s a simplified version of the Skupper usage guide, keeping all code snippets and adding brief explanations for each step:
#### 1. Install Skupper CLI

First, install the Skupper CLI. You can install it using the following command:

##### Install Skupper CLI (Linux version)
```
curl -sSL https://github.com/skupperproject/skupper/releases/download/v0.5.0/skupper-cli-v0.5.0-linux-amd64.tar.gz | tar -xvz
```

Explanation: This command downloads and extracts the Skupper CLI for Linux from GitHub. For other operating systems, refer to the official documentation.
#### 2. Install Skupper in Each Kubernetes Cluster

Install Skupper in both Kubernetes clusters and establish connections:

##### Install Skupper in Cluster A
```
kubectl create namespace skupper
skupper init
```

##### Install Skupper in Cluster B
```
kubectl create namespace skupper
skupper init
```

Explanation: Create a namespace called skupper and initialize Skupper in both clusters using skupper init. This installs all necessary resources for Skupper.
#### 3. Create a Connection Token in Cluster A

##### Generate a connection token in Cluster A
```
skupper token create token.txt
```

Explanation: This command creates a connection token in Cluster A, which will be used to configure the connection between the clusters.
#### 4. Use the Token in Cluster B to Establish the Connection

##### Use the connection token in Cluster B to connect to Cluster A
```
skupper token use token.txt
```

Explanation: Apply the token generated in Cluster A to Cluster B, establishing the connection between the two clusters.
#### 5. Expose Applications

Deploy and expose applications in both clusters.
Application in Cluster A:

##### Deploy an application and expose it
```
kubectl run myapp --image=nginx --port=8080
skupper expose deployment myapp --port 8080
```

Explanation: Deploy a simple Nginx application in Cluster A and expose it using Skupper to make it accessible within the network.
Application in Cluster B:

##### Deploy an application and expose it
```
kubectl run myapp --image=nginx --port=8080
skupper expose deployment myapp --port 8080
```

Explanation: Similarly, deploy an application in Cluster B and expose it, allowing the applications in both clusters to communicate with each other.

#### 6. Test the Connection

Verify if the connection between the two clusters is working:

##### Test accessing the service in Cluster B from Cluster A
```
kubectl run curl --image=curlimages/curl -i --tty --rm --restart=Never -- curl myapp.test.svc.cluster.local:8080
```

Explanation: This command runs a temporary curl container in Cluster A, trying to access the exposed service in Cluster B.
#### 7. Delete the Connection

If you want to delete the Skupper connection, run the following command:

##### Delete Skupper installation in Cluster A
```
skupper delete
```

##### Delete Skupper installation in Cluster B
```
skupper delete
```

Explanation: These commands delete all Skupper resources, including the connections between clusters.
