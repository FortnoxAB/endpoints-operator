# endpoints operator

Can be used to create endpoints for controller-manager och scheduler service objects. 
It will populate endpoints from node selectors and take the ports for the endpoints from the selected service. 

example config flags
```
-scheduler-node-label="node-role.kubernetes.io/controlplane=true" 
-scheduler-service=kube-system/kube-scheduler-prometheus-discovery 
-controller-manager-node-label="node-role.kubernetes.io/controlplane=true" 
-controller-manager-service=kube-system/kube-controller-manager-prometheus-discovery
```
