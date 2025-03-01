package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/pkg/errors"
	"github.com/spiffe/spire/cmd/spire-server/cli/run"
	"github.com/spiffe/spire/pkg/common/catalog"
	agentapi "github.com/spiffe/tornjak/tornjak-backend/api/agent"
	"github.com/urfave/cli/v2"
	"github.com/hashicorp/hcl"
)

type cliOptions struct {
	genericOptions struct {
		configFile  string // TODO change name
		tornjakFile string
	}
	httpOptions struct {
		listenAddr string
		certPath   string
		keyPath    string
		mtlsCaPath string
		tls        bool
		mtls       bool
	}
}

func main() {
	var opt cliOptions
	app := &cli.App{
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:        "spire-config-file",
				Aliases:     []string{"spire-config", "c"},
				Value:       "",
				Usage:       "Config file path for spire server",
				Destination: &opt.genericOptions.configFile,
				Required:    true,
			},
			&cli.StringFlag {
				Name:        "tornjak-config-file",
				Aliases:     []string{"tornjak-config", "t"},
				Value:       "",
				Usage:       "Config file path for tornjak server",
				Destination: &opt.genericOptions.tornjakFile,
				Required:    false,
			},
		},
		Commands: []*cli.Command{
			{
				Name:  "http",
				Usage: "Run the tornjak http server",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:        "listen-addr",
						Value:       ":10000",
						Usage:       "listening address for server",
						Destination: &opt.httpOptions.listenAddr,
						Required:    false,
					},
					&cli.StringFlag{
						Name:        "cert",
						Value:       "",
						Usage:       "CA Cert path for TLS",
						Destination: &opt.httpOptions.certPath,
						Required:    false,
					},
					&cli.StringFlag{
						Name:        "key",
						Value:       "",
						Usage:       "Key path for TLS",
						Destination: &opt.httpOptions.keyPath,
						Required:    false,
					},
					&cli.StringFlag{
						Name:        "mtls-ca",
						Value:       "",
						Usage:       "CA path for mTLS CA",
						Destination: &opt.httpOptions.mtlsCaPath,
						Required:    false,
					},
					&cli.BoolFlag{
						Name:        "tls",
						Value:       false,
						Usage:       "Enable TLS for http server",
						Destination: &opt.httpOptions.tls,
						Required:    false,
					},
					&cli.BoolFlag{
						Name:        "mtls",
						Value:       false,
						Usage:       "Enable mTLS for http server (overwrites tls flag)",
						Destination: &opt.httpOptions.mtls,
						Required:    false,
					},
				},

				Action: func(c *cli.Context) error {
					return runTornjakCmd("http", opt)
				},
			},
			{
				Name:  "serverinfo",
				Usage: "Get the serverinfo of the SPIRE server where tornjak resides",
				Action: func(c *cli.Context) error {
					return runTornjakCmd("serverinfo", opt)
				},
			},
		},
	}

	err := app.Run(os.Args)
	if err != nil {
		log.Fatal(err)
	}
}

func runTornjakCmd(cmd string, opt cliOptions) error {
	// parse configs
	config, err := run.ParseFile(opt.genericOptions.configFile, false)
	if err != nil {
		// Hide internal error since it is specific to arguments of originating library
		// i.e. asks to set -config which is a different flag in tornjak
		return errors.New("Unable to parse the config file provided")
	}
	tornjakConfigs, err := parseTornjakConfig(opt.genericOptions.tornjakFile)
	if err != nil {
		return errors.Errorf("Unable to parse the tornjak config file provided %v", err)
	}

	switch cmd {
	case "serverinfo":
		serverInfo, err := GetServerInfo(config)
		if err != nil {
			log.Fatalf("Error: %v", err)
		}
		fmt.Println(serverInfo)
		tornjakInfo, err := getTornjakConfig(opt.genericOptions.tornjakFile)
		if err != nil {
			log.Fatalf("Error: %v", err)
		}
		fmt.Println(tornjakInfo)
	case "http":
		serverInfo, err := GetServerInfo(config)
		if err != nil {
			log.Fatalf("Error: %v", err)
		}

		apiServer := &agentapi.Server{
			SpireServerAddr: getSocketPath(config),
			ListenAddr:      opt.httpOptions.listenAddr,
			CertPath:        opt.httpOptions.certPath,
			KeyPath:         opt.httpOptions.keyPath,
			MTlsCaPath:      opt.httpOptions.mtlsCaPath,
			TlsEnabled:      opt.httpOptions.tls,
			MTlsEnabled:     opt.httpOptions.mtls,
			SpireServerInfo: serverInfo,
			TornjakConfig:   tornjakConfigs,
		}
		apiServer.HandleRequests()
	default:
		return errors.New("Unrecognized command from helper func")
	}
	return nil

}

func GetServerInfo(config *run.Config) (agentapi.TornjakSpireServerInfo, error) {
	if config.Plugins == nil {
		return agentapi.TornjakSpireServerInfo{}, errors.New("config plugins map should not be nil")
	}

	pluginConfigs, err := catalog.PluginConfigsFromHCL(*config.Plugins)
	if err != nil {
		return agentapi.TornjakSpireServerInfo{}, errors.Errorf("Unable to parse plugin HCL: %v", err)
	}

	serverInfo := ""
	serverInfo += "Plugin Info\n"
	pluginMap := map[string][]string{}
	for _, pc := range pluginConfigs {
		serverInfo += fmt.Sprintf("%v Plugin: %v\n", pc.Type, pc.Name)
		serverInfo += fmt.Sprintf("Data: %v\n\n", pc.Data)
		pluginMap[pc.Type] = append(pluginMap[pc.Type], pc.Name)
	}

	serverInfo += "\n\n"
	serverInfo += "Server Info"
	s, _ := json.MarshalIndent(config.Server, "", "\t")
	serverInfo += string(s)

	return agentapi.TornjakSpireServerInfo{
		Plugins:       pluginMap,
		TrustDomain:   config.Server.TrustDomain,
		VerboseConfig: serverInfo,
	}, nil
}

func getSocketPath(config *run.Config) string {
	socketPath := config.Server.SocketPath
	if socketPath == "" {
		// TODO: temporary fix for issue with socket path resolution
		// using the defaultSocketPath in the SPIRE pkg, manually importing
		// since it is not a public variable.
		// https://github.com/spiffe/spire/blob/main/cmd/spire-server/cli/run/run.go#L44
		socketPath = "/tmp/spire-server/private/api.sock"
	}

	return "unix://" + socketPath
}

func getTornjakConfig(path string) (string, error) {
	if path == "" {
		return "", nil
	}

	// friendly error if file is missing
	byteData, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			absPath, err := filepath.Abs(path)
			if err != nil {
				msg := "could not determine CWD; tornjak config file not found at %s: use -tornjak-config"
				return "", fmt.Errorf(msg, path)
			}
			msg := "could not find tornjak config file %s: please use the -tornjak-config flag"
			return "", fmt.Errorf(msg, absPath)
		}
		return "", fmt.Errorf("unable to read tornjak configuration at %q: %w", path, err)
	}
	data := string(byteData)

	return data, nil
}

// below copied from spire/cmd/spire-server/cli/run/run.go, but with TornjakConfig
func parseTornjakConfig(path string) (*agentapi.TornjakConfig, error) {
	c := &agentapi.TornjakConfig{}

	if path == "" {
		return nil, nil
	}

	// friendly error if file is missing
	data, err := getTornjakConfig(path)
	if err != nil {
		return nil, err
	}

	if err := hcl.Decode(&c, data); err != nil {
		return nil, fmt.Errorf("unable to decode tornjak configuration at %q: %w", path, err)
	}

	return c, nil
}
