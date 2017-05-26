// Copyright 2015 Google Inc. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strings"

	"github.com/kubernetes/dashboard/src/app/backend/client"
	"github.com/kubernetes/dashboard/src/app/backend/handler"
	"github.com/prometheus/client_golang/prometheus"
	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/spf13/pflag"

	"github.com/dchest/captcha"
)

var (
	argPort          = pflag.Int("port", 9090, "The port to listen to for incoming HTTP requests")
	argBindAddress   = pflag.IP("bind-address", net.IPv4(0, 0, 0, 0), "The IP address on which to serve the --port (set to 0.0.0.0 for all interfaces).")
	argApiserverHost = pflag.String("apiserver-host", "", "The address of the Kubernetes Apiserver "+
		"to connect to in the format of protocol://address:port, e.g., "+
		"http://localhost:8080. If not specified, the assumption is that the binary runs inside a "+
		"Kubernetes cluster and local discovery is attempted.")
	argHeapsterHost = pflag.String("heapster-host", "", "The address of the Heapster Apiserver "+
		"to connect to in the format of protocol://address:port, e.g., "+
		"http://localhost:8082. If not specified, the assumption is that the binary runs inside a "+
		"Kubernetes cluster and service proxy will be used.")
	argPrometheusHost = pflag.String("prometheus-host", "", "The address of the Prometheus Apiserver "+
		"to connect to in the format of protocol://address:port, e.g., "+
		"http://localhost:9090. If not specified, the assumption is that the binary runs inside a "+
		"Kubernetes cluster and service proxy will be used.")
	mysqlHost         = pflag.String("mysql", "", "The address of the mysql.")
	argKubeConfigFile = pflag.String("kubeconfig", "", "Path to kubeconfig file with authorization and master location information.")
)

func main() {
	// Set logging output to standard console out
	log.SetOutput(os.Stdout)

	pflag.CommandLine.AddGoFlagSet(flag.CommandLine)
	pflag.Parse()
	flag.CommandLine.Parse(make([]string, 0)) // Init for glog calls in kubernetes packages

	log.Printf("Using HTTP port: %d", *argPort)
	if *argApiserverHost != "" {
		log.Printf("Using apiserver-host location: %s", *argApiserverHost)
	}
	if *argKubeConfigFile != "" {
		log.Printf("Using kubeconfig file: %s", *argKubeConfigFile)
	}

	apiserverClient, config, err := client.CreateApiserverClient(*argApiserverHost, *argKubeConfigFile)
	if err != nil {
		handleFatalInitError(err)
	}

	versionInfo, err := apiserverClient.ServerVersion()
	if err != nil {
		handleFatalInitError(err)
	}
	log.Printf("Successful initial request to the apiserver, version: %s", versionInfo.String())

	heapsterRESTClient, err := client.CreateHeapsterRESTClient(*argHeapsterHost, apiserverClient)
	if err != nil {
		log.Printf("Could not create heapster client: %s. Continuing.", err)
	}

	prometheusRESTClient, err := client.CreatePrometheusRESTClient(*argPrometheusHost, apiserverClient)
	if err != nil {
		log.Printf("Could not create prometheus client: %s. Continuing.", err)
	}
	// 获取mysql IP地址、端口、密码
	pod, err := apiserverClient.CoreV1().Pods("kube-system").List(metaV1.ListOptions{LabelSelector: "app=mysql"})
	if err != nil {
		handleFatalInitError(err)
	}

	mysqlConfig := strings.Join([]string{pod.Items[0].Status.HostIP, fmt.Sprintf("%d", pod.Items[0].Spec.Containers[0].Ports[0].ContainerPort)}, ":")
	//mysqlPwd := pod.Items[0].Spec.Containers[0].Env[0].Value
	pflag.Set("mysql", mysqlConfig)
	log.Println("mysql is", *mysqlHost)
	// make sure  database and table exist
	err = client.EnSureTableExist(*mysqlHost)
	if err != nil {
		log.Fatal(err)
	}
	// create mysql client return *mysql.DB
	mysqlClient, err := client.CreateMySQLConn(*mysqlHost)
	if err != nil {
		log.Fatal(err)
	}

	apiHandler, err := handler.CreateHTTPAPIHandler(apiserverClient, heapsterRESTClient, prometheusRESTClient, mysqlClient, config)
	if err != nil {
		handleFatalInitError(err)
	}
	/*
		// create prometheus config
		prom, err := api.NewClient(api.Config{Address: *argPrometheusHost})
		if err != nil {
			log.Fatalf("could not create prometheus http client: %s", err)
		}
		pro := v1.NewAPI(prom)
	*/

	// Run a HTTP server that serves static public files from './public' and handles API calls.
	// TODO(bryk): Disable directory listing.
	http.Handle("/", handler.MakeGzipHandler(handler.CreateLocaleHandler()))
	http.Handle("/api/", apiHandler)
	// TODO(maciaszczykm): Move to /appConfig.json as it was discussed in #640.
	http.Handle("/api/appConfig.json", handler.AppHandler(handler.ConfigHandler))
	http.Handle("/metrics", prometheus.Handler())
	http.Handle("/captcha", captcha.Server(captcha.StdWidth, captcha.StdHeight))
	// report
	/*http.HandleFunc("/report", func(w http.ResponseWriter, r *http.Request) {
		data, err := ioutil.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "report can not read data from body", http.StatusNoContent)
		}
		reports := make([]metric.Report, 5)

		err = json.Unmarshal(data, &reports)
		if err != nil {
			http.Error(w, "report can not unmarshal data", http.StatusUnprocessableEntity)
		}
		// 需要查询语句
		var reportMap = map[string][]metric.Report{
			"cluster": []metric.Report{},
			"node":    []metric.Report{},
			"app":     []metric.Report{},
			"pod":     []metric.Report{},
		}
		for _, report := range reports {
			query := report.Kind + report.Resource + report.Point
			value, err := pro.QueryRange(r.Context(), query, report.Range)
			if err != nil {
				http.Error(w, "report can not get data using queryrange", http.StatusUnprocessableEntity)
			}
			report.QueryData = model.Value(value)
			reportMap[report.Kind] = append(reportMap[report.Kind], report)

		}

	})
	*/

	// reporting forms

	log.Print(http.ListenAndServe(fmt.Sprintf("%s:%d", *argBindAddress, *argPort), nil))
}

/**
 * Handles fatal init error that prevents server from doing any work. Prints verbose error
 * message and quits the server.
 */
func handleFatalInitError(err error) {
	log.Fatalf("Error while initializing connection to Kubernetes apiserver. "+
		"This most likely means that the cluster is misconfigured (e.g., it has "+
		"invalid apiserver certificates or service accounts configuration) or the "+
		"--apiserver-host param points to a server that does not exist. Reason: %s\n"+
		"Refer to the troubleshooting guide for more information: "+
		"https://github.com/kubernetes/dashboard/blob/master/docs/user-guide/troubleshooting.md", err)
}
