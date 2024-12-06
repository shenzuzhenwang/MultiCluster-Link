#!/bin/bash

POD_NAME="iperf-client"
NAMESPACE="default"
CONTAINER_NAME="iperf"

start_time=$(date +%s)

kubectl exec $POD_NAME -n $NAMESPACE --container $CONTAINER_NAME -- /bin/sh -c "iptables -A OUTPUT -j DROP"

sleep 5

kubectl exec $POD_NAME -n $NAMESPACE --container $CONTAINER_NAME -- /bin/sh -c "iptables -D OUTPUT -j DROP"


while true; do
  kubectl exec $POD_NAME -n $NAMESPACE --container $CONTAINER_NAME -- ping -c 1 -W 1 8.8.8.8 &> /dev/null
  if [ $? -eq 0 ]; then
    end_time=$(date +%s)
    recovery_time=$((end_time - start_time))
    echo "Network connection restored successfully in $recovery_time seconds."
    break
  fi
  sleep 1
done