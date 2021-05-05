/*
Copyright 2019 The KubeOne Authors.

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

package state

import (
	"context"
	"path"
	"strings"

	"github.com/sirupsen/logrus"

	kubeoneapi "k8c.io/kubeone/pkg/apis/kubeone"
	"k8c.io/kubeone/pkg/configupload"
	"k8c.io/kubeone/pkg/runner"
	"k8c.io/kubeone/pkg/ssh"
	"k8c.io/kubeone/pkg/templates/images"

	apiserverconfigv1 "k8s.io/apiserver/pkg/apis/config/v1"
	"k8s.io/client-go/rest"
	bootstraputil "k8s.io/cluster-bootstrap/token/util"
	dynclient "sigs.k8s.io/controller-runtime/pkg/client"
	kyaml "sigs.k8s.io/yaml"
)

const (
	defaultEncryptionProvidersFile = "encryption-providers.yaml"
	customEncryptionProvidersFile  = "custom-encryption-providers.yaml"
)

func New(ctx context.Context) (*State, error) {
	joinToken, err := bootstraputil.GenerateBootstrapToken()
	s := &State{
		JoinToken:     joinToken,
		Connector:     ssh.NewConnector(ctx),
		Configuration: configupload.NewConfiguration(),
		Context:       ctx,
		WorkDir:       "./kubeone",
	}

	s.Images = images.NewResolver(
		images.WithOverwriteRegistryGetter(func() string {
			switch {
			case s.Cluster == nil:
				return ""
			case s.Cluster.RegistryConfiguration == nil:
				return ""
			}

			return s.Cluster.RegistryConfiguration.OverwriteRegistry
		}),
	)

	return s, err
}

// State holds together currently test flags and parsed info, along with
// utilities like logger
type State struct {
	Cluster                   *kubeoneapi.KubeOneCluster
	LiveCluster               *Cluster
	Logger                    logrus.FieldLogger
	Connector                 *ssh.Connector
	Configuration             *configupload.Configuration
	Images                    *images.Resolver
	Runner                    *runner.Runner
	Context                   context.Context
	WorkDir                   string
	JoinCommand               string
	JoinToken                 string
	RESTConfig                *rest.Config
	DynamicClient             dynclient.Client
	Verbose                   bool
	BackupFile                string
	DestroyWorkers            bool
	RemoveBinaries            bool
	ForceUpgrade              bool
	ForceInstall              bool
	UpgradeMachineDeployments bool
	PatchCNI                  bool
	CredentialsFilePath       string
	ManifestFilePath          string
	PauseImage                string
}

func (s *State) KubeadmVerboseFlag() string {
	if s.Verbose {
		return "--v=6"
	}
	return ""
}

// Clone returns a shallow copy of the State.
func (s *State) Clone() *State {
	newState := *s
	return &newState
}

func (s *State) ShouldDisableEncryption() bool {
	return (s.Cluster.Features.EncryptionProviders == nil ||
		!s.Cluster.Features.EncryptionProviders.Enable) &&
		s.LiveCluster.EncryptionConfiguration.Enable
}

func (s *State) ShouldEnableEncryption() bool {
	return s.Cluster.Features.EncryptionProviders != nil &&
		s.Cluster.Features.EncryptionProviders.Enable &&
		!s.LiveCluster.EncryptionConfiguration.Enable
}

func (s *State) EncryptionEnabled() bool {
	return s.Cluster.Features.EncryptionProviders != nil &&
		s.Cluster.Features.EncryptionProviders.Enable &&
		s.LiveCluster.EncryptionConfiguration.Enable
}

func (s *State) GetEncryptionProviderConfigName() string {
	if (s.ShouldEnableEncryption() && s.Cluster.Features.EncryptionProviders.CustomEncryptionConfiguration != "") ||
		s.LiveCluster.EncryptionConfiguration.Custom {
		return customEncryptionProvidersFile
	}
	return defaultEncryptionProvidersFile
}

func (s *State) GetKMSSocketPath() (string, error) {
	config := &apiserverconfigv1.EncryptionConfiguration{}
	// Custom configuration could be either on cluster side or the cluster configuration file
	// or both, depending on the enabled, enable/disable situation. We prefer the local configuration.
	if s.LiveCluster.CustomEncryptionEnabled() {
		config = s.LiveCluster.EncryptionConfiguration.Config
	} else {
		err := kyaml.UnmarshalStrict([]byte(s.Cluster.Features.EncryptionProviders.CustomEncryptionConfiguration), config)
		if err != nil {
			return "", err
		}
	}

	for _, r := range config.Resources {
		for _, p := range r.Providers {
			if p.KMS == nil {
				continue
			}
			return path.Clean(strings.ReplaceAll(p.KMS.Endpoint, "unix:", "")), nil
		}
	}

	return "", nil
}
