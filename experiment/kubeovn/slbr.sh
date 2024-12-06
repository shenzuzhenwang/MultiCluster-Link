#!/bin/bash

# Namespace for the services
NAMESPACE="ns2"

# Base name for the services
SERVICE_BASENAME="iperf-server"

# Create 10000 services
for i in $(seq 1 10); do
    # Generate service name
    SERVICE_NAME="${SERVICE_BASENAME}-${i}"
    VIP_OCTET=$((255 - i))
    VIP="242.5.255.${VIP_OCTET}"

    # Define the service manifest
    cat <<EOF | kubectl apply -f -
apiVersion: kubeovn.io/v1
kind: SwitchLBRule
metadata:
  name:  cjh-slr-iperf-${i}
spec:
  vip: ${VIP}
  sessionAffinity: None
  namespace: ns2
  selector:
    - app:iperf3-${i}
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
