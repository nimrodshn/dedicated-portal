package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/container-mgmt/dedicated-portal/pkg/signals"
	"github.com/container-mgmt/dedicated-portal/pkg/sql"
	"github.com/golang/glog"
	"github.com/gorilla/handlers"
	"github.com/gorilla/mux"
	"github.com/spf13/cobra"
	// "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Serve the clusters service service",
	Long:  "Serve the clusters service service.",
	Run:   runServe,
}

// Server serves HTTP API requests on clusters.
type Server struct {
	stopCh         <-chan struct{}
	clusterService ClustersService
}

// NewServer creates a new server.
func NewServer(stopCh <-chan struct{}, clusterService ClustersService) *Server {
	server := new(Server)
	server.stopCh = stopCh
	server.clusterService = clusterService
	return server
}

func (s Server) start() error {
	// Create the main router:
	mainRouter := mux.NewRouter()

	// Create the API router:
	apiRouter := mainRouter.PathPrefix("/api/clusters_mgmt/v1").Subrouter()
	apiRouter.HandleFunc("/clusters", s.listClusters).Methods("GET")
	apiRouter.HandleFunc("/clusters", s.createCluster).Methods("POST")
	apiRouter.HandleFunc("/clusters/{uuid}", s.getCluster).Methods("GET")

	// Enable the access log:
	loggedRouter := handlers.LoggingHandler(os.Stdout, mainRouter)

	fmt.Println("Listening.")
	go http.ListenAndServe(":8000", loggedRouter)
	return nil
}

var (
	serverKubeAddress string
	serverKubeConfig  string
)

func init() {
	serveFlags := serveCmd.Flags()
	serveFlags.StringVar(
		&serverKubeConfig,
		"kubeconfig",
		"",
		"Path to a Kubernetes client configuration file. Only required when running "+
			"cluster-operator outside of a cluster.",
	)
	serveFlags.StringVar(
		&serverKubeAddress,
		"master",
		"",
		"The address of the Kubernetes API server. Overrides any value in the Kubernetes "+
			"configuration file. Only required when running cluster-operator outside of a cluster.",
	)
}

func kubeConfigPath(serverKubeConfig string) (kubeConfig string, err error) {
	// The loading order follows these rules:
	// 1. If the –kubeconfig flag is set,
	// then only that file is loaded. The flag may only be set once.
	// 2. If $KUBECONFIG environment variable is set, use it.
	// 3. Otherwise, ${HOME}/.kube/config is used.
	var ok bool

	// Get the config file path
	if serverKubeConfig != "" {
		kubeConfig = serverKubeConfig
	} else {
		if kubeConfig, ok = os.LookupEnv("KUBECONFIG"); ok != true {
			kubeConfig = filepath.Join(homedir.HomeDir(), ".kube", "config")
		}
	}

	// Check config file:
	fInfo, err := os.Stat(kubeConfig)
	if os.IsNotExist(err) {
		// NOTE: If config file does not exist, assume using pod configuration.
		err = fmt.Errorf("The Kubernetes configuration file '%s' doesn't exist", kubeConfig)
		kubeConfig = ""
		return
	}

	// Check error codes.
	if fInfo.IsDir() {
		err = fmt.Errorf("The Kubernetes configuration path '%s' is a direcory", kubeConfig)
		return
	}
	if os.IsPermission(err) {
		err = fmt.Errorf("Can't open Kubernetes configuration file '%s'", kubeConfig)
		return
	}

	return
}

func runServe(cmd *cobra.Command, args []string) {
	// Set up signals so we handle the first shutdown signal gracefully:
	stopCh := signals.SetupHandler()

	// Load the Kubernetes configuration:
	var config *rest.Config

	kubeConfig, err := kubeConfigPath(serverKubeConfig)
	if err == nil {
		// If error is nil, we have a valid kubeConfig file:
		config, err = clientcmd.BuildConfigFromFlags(serverKubeAddress, kubeConfig)
		if err != nil {
			glog.Fatalf(
				"Error loading REST client configuration from file '%s': %s",
				kubeConfig, err,
			)
		}
	} else if kubeConfig == "" {
		// If kubeConfig is "", file is missing, in this case we will
		// try to use in-cluster configuration.
		glog.Info("Try to use the in-cluster configuration")
		config, err = rest.InClusterConfig()

		// Catch in-cluster configuration error:
		if err != nil {
			glog.Fatalf("Error loading in-cluster REST client configuration: %s", err)
		}
	} else {
		// Catch all errors:
		glog.Fatalf("Error: %s", err)
	}

	url := ConnectionURL()
	err = sql.EnsureSchema(
		"/usr/local/share/clusters-service/migrations",
		url,
	)
	if err != nil {
		panic(err)
	}

	provisioner := NewClusterOperatorProvisioner(config)

	service := NewClustersService(url, provisioner)
	fmt.Println("Created cluster service.")

	// This is temporary and should be replaced with reading from the queue
	server := NewServer(stopCh, service)
	err = server.start()
	if err != nil {
		panic(fmt.Sprintf("Error starting server: %v", err))
	}
	fmt.Println("Created server.")

	fmt.Println("Waiting for stop signal")
	<-stopCh // wait until requested to stop.
}

// ConnectionURL generates a connection string from the environment.
func ConnectionURL() string {
	return fmt.Sprintf("postgres://%s:%s@localhost:5432/%s?sslmode=disable",
		os.Getenv("POSTGRESQL_USER"),
		os.Getenv("POSTGRESQL_PASSWORD"),
		os.Getenv("POSTGRESQL_DATABASE"))
}
