#!/bin/bash

client_pods=$(kubectl get pods -l app=iperf-client -o custom-columns=":metadata.name")

for pod in $client_pods; do
  kubectl logs $pod >> iperf_results.txt
done