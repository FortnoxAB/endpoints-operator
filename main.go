package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fortnoxab/fnxlogrus"
	"github.com/jonaz/gograce"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

var metricsAddr = flag.String("listen-address", ":8080", "The address to listen on for HTTP metrics requests.")
var logLevel = flag.String("log-level", "info", "loglevel")

var kubeClient corev1client.CoreV1Interface

func main() {
	flag.Parse()
	fnxlogrus.Init(fnxlogrus.Config{Format: "json", Level: *logLevel}, logrus.StandardLogger())

	kubeClient = getKubeClient()

	http.Handle("/metrics", promhttp.Handler())
	srv, shutdown := gograce.NewServerWithTimeout(10 * time.Second)
	srv.Handler = http.DefaultServeMux
	srv.Addr = *metricsAddr

	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer wg.Done()
		err := srv.ListenAndServe()
		if err != nil && err != http.ErrServerClosed {
			logrus.Error(err)
		}
	}()

	go periodicSyncer(shutdown)
	<-shutdown
	wg.Wait()
}

func periodicSyncer(stopc <-chan struct{}) {
	syncAndLog()
	ticker := time.NewTicker(2 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-stopc:
			return
		case <-ticker.C:
			syncAndLog()
		}
	}
}

var endpointsSyncEnabledService = metav1.LabelSelector{MatchLabels: map[string]string{"endpoints-operator.fnox.se/enabled": "true"}}

const nodeSelectorLabel = "endpoints-operator.fnox.se/node-selector"

func syncAndLog() {
	servicesToCheck, err := kubeClient.Services("").List(metav1.ListOptions{
		LabelSelector: labels.Set(endpointsSyncEnabledService.MatchLabels).String(),
	})
	if err != nil {
		logrus.Error(err)
		return
	}

	for _, svc := range servicesToCheck.Items {
		nodeSelector, ok := svc.Annotations[nodeSelectorLabel]
		if !ok {
			logrus.Errorf("missing %s label on service %s", nodeSelectorLabel, svc.GetName())
			continue
		}

		err := syncNodeEndpoints(svc.GetNamespace(), svc.GetName(), nodeSelector)
		if err != nil {
			logrus.Error(err)
		}
	}
}

func syncNodeEndpoints(namespace, svc, nodeSelector string) error {
	logrus.Debugf("starting sync of %s", svc)
	service, err := kubeClient.Services(namespace).Get(svc, metav1.GetOptions{})
	if err != nil {
		return err
	}

	eps := &v1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name:   service.Name,
			Labels: service.Labels,
		},
		Subsets: []v1.EndpointSubset{
			{
				Ports: []v1.EndpointPort{},
			},
		},
	}

	for _, port := range service.Spec.Ports {
		eps.Subsets[0].Ports = append(eps.Subsets[0].Ports, v1.EndpointPort{
			Name: port.Name,
			Port: port.Port,
		})
	}

	nodes, err := kubeClient.Nodes().List(metav1.ListOptions{LabelSelector: nodeSelector})
	if err != nil {
		return errors.Wrap(err, "listing nodes failed")
	}

	addresses, errs := getNodeAddresses(nodes)
	if len(errs) > 0 {
		for _, err := range errs {
			logrus.Warnf("error getting node address: %s", err)
		}
	}
	eps.Subsets[0].Addresses = addresses

	err = CreateOrUpdateEndpoints(kubeClient.Endpoints(service.GetNamespace()), eps)
	if err != nil {
		return errors.Wrap(err, "synchronizing kubelet endpoints object failed")
	}

	return nil
}

func CreateOrUpdateEndpoints(eclient corev1client.EndpointsInterface, eps *v1.Endpoints) error {
	endpoints, err := eclient.Get(eps.Name, metav1.GetOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return errors.Wrap(err, "retrieving existing kubelet endpoints object failed")
	}

	if apierrors.IsNotFound(err) {
		_, err = eclient.Create(eps)
		if err != nil {
			return errors.Wrap(err, "creating kubelet endpoints object failed")
		}
	} else {
		eps.ResourceVersion = endpoints.ResourceVersion
		_, err = eclient.Update(eps)
		if err != nil {
			return errors.Wrap(err, "updating kubelet endpoints object failed")
		}
	}

	return nil
}

func getNodeAddresses(nodes *v1.NodeList) ([]v1.EndpointAddress, []error) {
	addresses := make([]v1.EndpointAddress, 0)
	errs := make([]error, 0)

	for _, n := range nodes.Items {
		address, _, err := nodeAddress(n)
		if err != nil {
			errs = append(errs, errors.Wrapf(err, "failed to determine hostname for node (%s)", n.Name))
			continue
		}
		addresses = append(addresses, v1.EndpointAddress{
			IP: address,
			TargetRef: &v1.ObjectReference{
				Kind:       "Node",
				Name:       n.Name,
				UID:        n.UID,
				APIVersion: n.APIVersion,
			},
		})
	}

	return addresses, errs
}

func getKubeClient() corev1client.CoreV1Interface {
	var kubeconfig string
	if os.Getenv("KUBECONFIG") != "" {
		kubeconfig = os.Getenv("KUBECONFIG")
	} else if home := homedir.HomeDir(); home != "" {
		kubeconfig = filepath.Join(home, ".kube", "config")
	}
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		logrus.Info("No kubeconfig found. Using incluster...")
		config, err = rest.InClusterConfig()
		if err != nil {
			panic(err.Error())
		}
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		logrus.Error("error kubernetes.NewForConfig")
		panic(err)
	}
	return clientset.CoreV1()
}

// nodeAddresses returns the provided node's address, based on the priority:
// 1. NodeInternalIP
// 2. NodeExternalIP
//
// Copied from github.com/prometheus/prometheus/discovery/kubernetes/node.go
func nodeAddress(node v1.Node) (string, map[v1.NodeAddressType][]string, error) {
	m := map[v1.NodeAddressType][]string{}
	for _, a := range node.Status.Addresses {
		m[a.Type] = append(m[a.Type], a.Address)
	}

	if addresses, ok := m[v1.NodeInternalIP]; ok {
		return addresses[0], m, nil
	}
	if addresses, ok := m[v1.NodeExternalIP]; ok {
		return addresses[0], m, nil
	}
	return "", m, fmt.Errorf("host address unknown")
}
