/*
Copyright (c) 2018 Red Hat, Inc.

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

//nolint
//go:generate python -c "import json, sys, yaml; y=yaml.safe_load(open(\"./data/swagger/customers-service.yaml\")); open(\"./data/swagger/customers-service.json\",\"w\").write(json.dumps(y))"
//nolint
//go:generate go-bindata -o ./data/generated/migrations/migrations.go -pkg migrations -prefix data/migrations/ ./data/migrations
//go:generate go-bindata -o ./data/generated/swagger/openapi.go -pkg openapi -prefix data/swagger/ ./data/swagger

package main

import (
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/golang/glog"
	"github.com/gorilla/handlers"
	"github.com/gorilla/mux"
	"github.com/spf13/cobra"

	"github.com/container-mgmt/dedicated-portal/cmd/customers-service/service"
	"github.com/container-mgmt/dedicated-portal/pkg/auth"
	"github.com/container-mgmt/dedicated-portal/pkg/sql"

	//nolint
	"github.com/container-mgmt/dedicated-portal/cmd/customers-service/data/generated/migrations"
	//nolint
	"github.com/container-mgmt/dedicated-portal/cmd/customers-service/data/generated/swagger"
)

var serveArgs struct {
	host          string
	port          int
	jwkCertURL    string
	dbURL         string
	demoMode      string
	noHTTPS       bool
	httpsCertPath string
	httpsKeyPath  string
}

// A static json file containing the openAPI json definitions
var openAPIdefinitions []byte

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Serve the customers service",
	Long:  "Serve the customers service.",
	Run:   runServe,
}

func init() {
	flags := serveCmd.Flags()
	flags.StringVar(
		&serveArgs.host,
		"host",
		"0.0.0.0",
		"The IP address or host name of the server.",
	)
	flags.IntVar(
		&serveArgs.port,
		"port",
		8000,
		"The port number of the server.",
	)
	flags.StringVar(
		&serveArgs.dbURL,
		"db-url",
		"",
		"The database connection url.",
	)
	flags.StringVar(
		&serveArgs.jwkCertURL,
		"jwk-certs-url",
		"",
		"The url endpoint for the JWK certs.",
	)
	flags.StringVar(
		&serveArgs.demoMode,
		"demo-mode",
		"false",
		"If set to \"true\" run in demo mode (no token needed, return demo data).",
	)
	flags.BoolVar(
		&serveArgs.noHTTPS,
		"no-https",
		false,
		"Serve without using tls.",
	)
	flags.StringVar(
		&serveArgs.httpsCertPath,
		"https-cert-path",
		"",
		"The path to the tls.crt file.",
	)
	flags.StringVar(
		&serveArgs.httpsKeyPath,
		"https-key-path",
		"",
		"The path to the tls.key file.",
	)
}

func runServe(cmd *cobra.Command, args []string) {
	var err error
	var s service.CustomersService
	var loggedRouter http.Handler

	// Try to connect to SQLCustomersService
	//
	// If not in demo mode, try to connect to the sql server.
	// If we are in demo mode, connect to a demo data source.
	if serveArgs.demoMode != "true" {
		// Check for db url cli arg:
		if serveArgs.dbURL == "" {
			check(fmt.Errorf("flag missing: --db-url"), "No db URL defined")
		}

		err = sql.EnsureSchema(serveArgs.dbURL, migrations.AssetNames, migrations.Asset)
		if err != nil {
			check(err, "Can't migrate sql schema")
		}

		// Connect to the SQL service.
		s, err = service.NewSQLCustomersService(serveArgs.dbURL)
	} else {
		// Connect to the Demo service.
		s, err = service.NewDemoCustomersService()
	}
	check(err, "Can't connect to sql service")
	defer s.Close()

	// Create server URL.
	serverAddress := fmt.Sprintf("%s:%d", serveArgs.host, serveArgs.port)

	// Start server.
	server := NewServer(s)
	defer server.Close()

	// Create the main router:
	mainRouter := mux.NewRouter()

	// Create the API router:
	apiRouter := mainRouter.PathPrefix("/api/customers_mgmt/v1").Subrouter()
	apiRouter.HandleFunc("/customers", server.getCustomersList).Methods(http.MethodGet)
	apiRouter.HandleFunc("/customers", server.addCustomer).Methods(http.MethodPost)
	apiRouter.HandleFunc("/customers/{id}", server.getCustomerByID).Methods(http.MethodGet)
	apiRouter.HandleFunc("/openapi", getOpenAPI).Methods(http.MethodGet)

	apiRouter.Path("/customers").
		Queries("page", "{[0-9]+}", "size", "{[0-9]+}").
		Methods(http.MethodGet).
		HandlerFunc(server.getCustomersList)

	// If not in demo mode, check JWK and add a JWT middleware:
	//
	// When running on demo mode we want to bypass the JWT check
	// and serve mock data.
	if serveArgs.demoMode != "true" {
		// Check for JWK cert cli arg:
		if serveArgs.jwkCertURL == "" {
			check(fmt.Errorf("flag missing: --jwk-certs-url"), "No cert URL defined")
		}

		// Enable the access authentication:
		authRouter, err := auth.Router(serveArgs.jwkCertURL, mainRouter)
		check(
			err,
			fmt.Sprintf(
				"Can't create authentication route using URL '%s'",
				serveArgs.jwkCertURL,
			),
		)

		// Enable the access log:
		loggedRouter = handlers.LoggingHandler(os.Stdout, authRouter)
	} else {
		// On demo mode, just log requests:

		// Enable the access log:
		loggedRouter = handlers.LoggingHandler(os.Stdout, mainRouter)
	}

	// Try to load openAPI data
	openAPIdefinitions, err = openapi.Asset("customers-service.json")
	if err != nil {
		check(err, "Can't load openAPI definitions")
	}

	// Inform user we are starting.
	glog.Infof("Starting customers-service server at %s.", serverAddress)

	// Create the http server
	srv := &http.Server{
		Addr:    serverAddress,
		Handler: loggedRouter,
	}

	// ListenAndServe
	if serveArgs.noHTTPS {
		// Serve without TLS
		log.Fatal(srv.ListenAndServe())
	} else {
		// Check https cert and key path path
		if serveArgs.httpsCertPath == "" || serveArgs.httpsKeyPath == "" {
			check(
				fmt.Errorf("Unspecified required --https-cert-path, --https-key-path"),
				"Can't start https server",
			)
		}

		// Serve with TLS
		log.Fatal(srv.ListenAndServeTLS(serveArgs.httpsCertPath, serveArgs.httpsKeyPath))
	}
}

// write openAPI respinse
func getOpenAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Send response body
	_, err := w.Write(openAPIdefinitions)
	if err != nil {
		glog.Errorf("Write to client: %s", err)
	}
}

// Exit on error
func check(err error, msg string) {
	if err != nil {
		glog.Errorf("%s: %s", msg, err)
		os.Exit(1)
	}
}
