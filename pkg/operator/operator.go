/*
Copyright © 2018 inwinSTACK.inc

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

   http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package operator

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/golang/glog"
	clientset "github.com/inwinstack/blended/client/clientset/versioned"
	opkit "github.com/inwinstack/operator-kit"
	"github.com/inwinstack/pa-controller/pkg/config"
	"github.com/inwinstack/pa-controller/pkg/k8sutil"
	"github.com/inwinstack/pa-controller/pkg/operator/pa"
	"github.com/inwinstack/pa-controller/pkg/operator/pa/nat"
	"github.com/inwinstack/pa-controller/pkg/operator/pa/security"
	"github.com/inwinstack/pa-controller/pkg/operator/pa/service"
	"k8s.io/api/core/v1"

	apiextensionsclientset "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	"k8s.io/client-go/kubernetes"
)

const (
	initRetryDelay = 10 * time.Second
	interval       = 500 * time.Millisecond
	timeout        = 60 * time.Second
)

type Operator struct {
	ctx        *opkit.Context
	conf       *config.OperatorConfig
	resources  []opkit.CustomResource
	controller *pa.Controller
}

func NewMainOperator(conf *config.OperatorConfig) *Operator {
	return &Operator{
		resources: []opkit.CustomResource{
			nat.Resource,
			security.Resource,
			service.Resource,
		},
		conf: conf,
	}
}

func (o *Operator) Initialize() error {
	glog.V(2).Info("Initialize the operator resources.")

	ctx, blendedClient, err := o.initContextAndClient()
	if err != nil {
		return err
	}

	o.controller = pa.NewController(ctx, blendedClient, o.conf)
	o.ctx = ctx
	return nil
}

func (o *Operator) initContextAndClient() (*opkit.Context, clientset.Interface, error) {
	glog.V(2).Info("Initialize the operator context and client.")

	config, err := k8sutil.GetRestConfig(o.conf.Kubeconfig)
	if err != nil {
		return nil, nil, fmt.Errorf("Failed to get Kubernetes config. %+v", err)
	}

	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, nil, fmt.Errorf("Failed to get Kubernetes client. %+v", err)
	}

	extensionsClient, err := apiextensionsclientset.NewForConfig(config)
	if err != nil {
		return nil, nil, fmt.Errorf("Failed to create Kubernetes API extension clientset. %+v", err)
	}

	blendedClient, err := clientset.NewForConfig(config)
	if err != nil {
		return nil, nil, fmt.Errorf("Failed to create blended clientset. %+v", err)
	}

	ctx := &opkit.Context{
		Clientset:             client,
		APIExtensionClientset: extensionsClient,
		Interval:              interval,
		Timeout:               timeout,
	}
	return ctx, blendedClient, nil
}

func (o *Operator) initResources() error {
	glog.V(2).Info("Initialize the CRD resources.")

	ctx := opkit.Context{
		Clientset:             o.ctx.Clientset,
		APIExtensionClientset: o.ctx.APIExtensionClientset,
		Interval:              interval,
		Timeout:               timeout,
	}

	if err := opkit.CreateCustomResources(ctx, o.resources); err != nil {
		return fmt.Errorf("Failed to create custom resource. %+v", err)
	}
	return nil
}

func (o *Operator) Run() error {
	for {
		err := o.initResources()
		if err == nil {
			break
		}
		glog.Errorf("Failed to init resources. %+v. retrying...", err)
		<-time.After(initRetryDelay)
	}

	signalChan := make(chan os.Signal, 1)
	stopChan := make(chan struct{})
	signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM)

	if err := o.controller.Initialize(); err != nil {
		return fmt.Errorf("Failed to init PA controller. %+v", err)
	}

	// start watching the resources
	o.controller.StartWatch(v1.NamespaceAll, stopChan)

	for {
		select {
		case <-signalChan:
			glog.Infof("Shutdown signal received, exiting...")
			close(stopChan)
			return nil
		}
	}
}
