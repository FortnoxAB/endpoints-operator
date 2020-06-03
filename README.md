# endpoints operator

Can be used to create endpoints for controller-manager och scheduler service objects. 
It will populate endpoints from node selectors and take the ports for the endpoints from the selected service. 

It fetches config from Service object
Example:
```
apiVersion: v1
kind: Service
metadata:
  annotations:
    endpoints-operator.fnox.se/node-selector: node-role.kubernetes.io/controlplane=true
  labels:
    endpoints-operator.fnox.se/enabled: "true"
    k8s-app: kube-controller-manager
  name: kube-controller-manager-prometheus-discovery
  namespace: kube-system
spec:
  clusterIP: None
  ports:
  - name: http-metrics
    port: 10252
    targetPort: 10252
  selector:
    k8s-app: kube-controller-manager
```
