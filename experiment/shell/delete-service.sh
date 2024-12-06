#!/bin/bash

# Namespace for the services
NAMESPACE="test"

# Base name for the services
SERVICE_BASENAME="my-service"

# Delete 10000 services
for i in $(seq 1 1000); do
    # Generate service name
    SERVICE_NAME="${SERVICE_BASENAME}-${i}"

    # Delete the service
    kubectl delete service ${SERVICE_NAME} -n ${NAMESPACE}

    # Optional: Add a delay to avoid hitting API rate limits
    #sleep 0.01
done
