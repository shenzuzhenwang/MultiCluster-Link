#!/bin/bash

# Namespace for the services
NAMESPACE="ns2"

# Base name for the services
SERVICE_BASENAME="iperf-server"

# Create 10000 services
for i in $(seq 1 10); do
    # Generate service name
    SERVICE_NAME="${SERVICE_BASENAME}-${i}"

    # Define the service manifest
    cat <<EOF | kubectl apply -f -
apiVersion: apps/v1
kind: Deployment
metadata:
  name: iperf3-server-${i}
  annotations:
    ovn.kubernetes.io/logical_switch: vpc2-net1
  namespace: ns2
  labels:
    app: iperf3-${i}
spec:
  replicas: 1
  selector:
    matchLabels:
      app: iperf3-${i}
  template:
    metadata:
      labels:
        app: iperf3-${i}
    spec:
      containers:
      - name: iperf3
        image: networkstatic/iperf3
        command: ["/bin/sh", "-c", "iperf3 -s -p 5201"]
        imagePullPolicy: IfNotPresent
---
apiVersion: v1
kind: Service
metadata:
  name: iperf3-server-2${i}
  namespace: ns2
  labels:
    app: iperf3-${i}
spec:
  selector:
    app: iperf3-${i}
  ports:
    - name: tcp-5201
      protocol: TCP
      port: 5201
      targetPort: 5201
    - name: udp-5201
      protocol: UDP
      port: 5201
      targetPort: 5201
EOF

    # Optional: Add a delay to avoid hitting API rate limits
    sleep 0.01
done
