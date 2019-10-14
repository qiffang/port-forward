# Introduction
The purpose of this tool is to provide external TiDB connect Kubernetes TiDB Cluster. 
This is especially useful to debug Kubernetes TiDB Cluster.

# Quick start
## Step1 - Build the current project
```$xslt
 go build -o forward cmd/main/main.go
```

## Step2 - Run the binary
```$xslt
sudo ./forward --path=/Users/andy/.kube/config --namespace=$namesapce
```

*The binary need to run with **root** user.*

If it start successful, the log will show **pdList**, then the external TiDB can join the Kubernetes TiDB Cluster.
```$xslt
Start...
Forwarding from 127.0.0.1:51554 -> 20160
Forwarding from [::1]:51554 -> 20160
Forwarding from 127.0.0.1:51565 -> 20160
Forwarding from [::1]:51565 -> 20160
Forwarding from 127.0.0.1:51578 -> 2379
Forwarding from [::1]:51578 -> 2379
pdList: test-pd-0.test-pd-peer.test-cs.svc:2379,test-pd-1.test-pd-peer.test-cs.svc:2379
```

## Step3 - External TiDB join in Kubernetes TiDB Cluster
```$xslt
 bin/tidb-server --store=tikv --path="$pdList(in the log of step2)"

```
*The external TiDB need to run the same server with the forward tool.*