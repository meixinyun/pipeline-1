// Copyright © 2019 Banzai Cloud
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

package logging

import (
	"github.com/banzaicloud/logging-operator/pkg/sdk/api/v1beta1"
	"github.com/banzaicloud/logging-operator/pkg/sdk/model/output"
	loggingSecret "github.com/banzaicloud/logging-operator/pkg/sdk/model/secret"
)

type outputDefinitionManagerGCS struct {
	baseOutputManager
}

func (outputDefinitionManagerGCS) getOutputName() string {
	return "gcs-output"
}

func (outputDefinitionManagerGCS) getFlowName() string {
	return "gcs-flow"
}

func (m outputDefinitionManagerGCS) getOutputSpec(spec clusterOutputSpec, op bucketOptions) v1beta1.ClusterOutputSpec {
	return v1beta1.ClusterOutputSpec{
		OutputSpec: v1beta1.OutputSpec{
			GCSOutput: &output.GCSOutput{
				Project: op.gcs.project,
				Keyfile: "",
				CredentialsJson: &loggingSecret.Secret{
					ValueFrom: &loggingSecret.ValueFrom{
						SecretKeyRef: &loggingSecret.KubernetesSecret{
							Name: m.sourceSecretName,
							Key:  outputDefinitionSecretKeyGCS,
						},
					},
				},
				Bucket: spec.Provider.Bucket.Name,
				Path:   m.getPathSpec(),
				Buffer: m.getBufferSpec(),
			},
		},
	}
}