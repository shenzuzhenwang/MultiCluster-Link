for i in $(seq 1 10); do
    subctl export service -n ns2 iperf3-server-2${i}

    # Optional: Add a delay to avoid hitting API rate limits
    sleep 0.01
done