#!/bin/bash

# Namespace for the services
NAMESPACE="test"

# Base name for the services
SERVICE_BASENAME="my-service"

# Create 10000 services
for i in $(seq 1 100); do
    # Generate service name
    SERVICE_NAME="${SERVICE_BASENAME}-${i}"

    # Define the service manifest
    cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: Service
metadata:
  name: ${SERVICE_NAME}
  namespace: ${NAMESPACE}
spec:
  selector:
    app: my-app-${i}
  ports:
    - protocol: TCP
      port: 80
      targetPort: 8080
EOF

    # Optional: Add a delay to avoid hitting API rate limits
    #sleep 0.01
done
