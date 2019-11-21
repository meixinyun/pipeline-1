// Copyright © 2018 Banzai Cloud
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

package cluster

import (
	"encoding/json"
	"fmt"
	"strings"

	"emperror.dev/emperror"
	"github.com/ghodss/yaml"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	k8sHelm "k8s.io/helm/pkg/helm"
	pkgHelmRelease "k8s.io/helm/pkg/proto/hapi/release"

	"github.com/banzaicloud/pipeline/auth"
	"github.com/banzaicloud/pipeline/helm"
	arkAPI "github.com/banzaicloud/pipeline/internal/ark/api"
	arkPosthook "github.com/banzaicloud/pipeline/internal/ark/posthook"
	"github.com/banzaicloud/pipeline/internal/global"
	"github.com/banzaicloud/pipeline/internal/hollowtrees"
	pkgCluster "github.com/banzaicloud/pipeline/pkg/cluster"
	"github.com/banzaicloud/pipeline/pkg/cluster/pke"
	pkgCommon "github.com/banzaicloud/pipeline/pkg/common"
	pkgHelm "github.com/banzaicloud/pipeline/pkg/helm"
	"github.com/banzaicloud/pipeline/pkg/k8sclient"
	"github.com/banzaicloud/pipeline/pkg/k8sutil"
)

func castToPostHookParam(data *pkgCluster.PostHookParam, output interface{}) (err error) {

	var bytes []byte
	bytes, err = json.Marshal(data)
	if err != nil {
		return
	}

	err = json.Unmarshal(bytes, &output)

	return
}

func installDeployment(cluster CommonCluster, namespace string, deploymentName string, releaseName string, values []byte, chartVersion string, wait bool) error {
	// --- [ Get K8S Config ] --- //
	kubeConfig, err := cluster.GetK8sConfig()
	if err != nil {
		log.Errorf("Unable to fetch config for posthook: %s", err.Error())
		return err
	}

	org, err := auth.GetOrganizationById(cluster.GetOrganizationId())
	if err != nil {
		log.Errorf("Error during getting organization: %s", err.Error())
		return err
	}

	deployments, err := helm.ListDeployments(&releaseName, "", kubeConfig)
	if err != nil {
		log.Errorln("Unable to fetch deployments from helm:", err)
		return err
	}

	var foundRelease *pkgHelmRelease.Release

	if deployments != nil {
		for _, release := range deployments.Releases {
			if release.Name == releaseName {
				foundRelease = release
				break
			}
		}
	}

	if foundRelease != nil {
		switch foundRelease.GetInfo().GetStatus().GetCode() {
		case pkgHelmRelease.Status_DEPLOYED:
			log.Infof("'%s' is already installed", deploymentName)
			return nil
		case pkgHelmRelease.Status_FAILED:
			err = helm.DeleteDeployment(releaseName, kubeConfig)
			if err != nil {
				log.Errorf("Failed to deleted failed deployment '%s' due to: %s", deploymentName, err.Error())
				return err
			}
		}
	}

	options := []k8sHelm.InstallOption{
		k8sHelm.InstallWait(wait),
		k8sHelm.ValueOverrides(values),
	}
	_, err = helm.CreateDeployment(deploymentName, chartVersion, nil, namespace, releaseName, false, nil, kubeConfig, helm.GenerateHelmRepoEnv(org.Name), options...)
	if err != nil {
		log.Errorf("Deploying '%s' failed due to: %s", deploymentName, err.Error())
		return err
	}
	log.Infof("'%s' installed", deploymentName)
	return nil
}

func InstallKubernetesDashboardPostHook(cluster CommonCluster) error {

	k8sDashboardNameSpace := global.Config.Cluster.Namespace
	k8sDashboardReleaseName := "dashboard"
	var valuesJson []byte

	if cluster.RbacEnabled() {

		// create service account
		kubeConfig, err := cluster.GetK8sConfig()
		if err != nil {
			log.Errorf("Unable to fetch config for posthook: %s", err.Error())
			return err
		}

		client, err := k8sclient.NewClientFromKubeConfig(kubeConfig)
		if err != nil {
			log.Errorf("Could not get kubernetes client: %s", err)
			return err
		}

		// service account
		k8sDashboardServiceAccountName := k8sDashboardReleaseName
		serviceAccount, err := k8sutil.GetOrCreateServiceAccount(log, client, k8sDashboardNameSpace, k8sDashboardServiceAccountName)
		if err != nil {
			return err
		}

		// cluster role based on https://github.com/helm/charts/blob/master/stable/kubernetes-dashboard/templates/role.yaml
		clusterRoleName := k8sDashboardReleaseName
		rules := []rbacv1.PolicyRule{
			// Allow to list all
			{
				APIGroups: []string{"*"},
				Resources: []string{"*"},
				Verbs:     []string{"list", "get"},
			},
			// # Allow Dashboard to create 'kubernetes-dashboard-key-holder' secret.
			{
				APIGroups: []string{""},
				Resources: []string{"secrets"},
				Verbs:     []string{"create"},
			},
			// # Allow Dashboard to list and create 'kubernetes-dashboard-settings' config map.
			{
				APIGroups: []string{""},
				Resources: []string{"configmaps"},
				Verbs:     []string{"create"},
			},
			// # Allow Dashboard to get, update and delete Dashboard exclusive secrets.
			{
				APIGroups:     []string{""},
				Resources:     []string{"secrets"},
				ResourceNames: []string{"kubernetes-dashboard-key-holder", fmt.Sprintf("kubernetes-dashboard-%s", k8sDashboardReleaseName)},
				Verbs:         []string{"update", "delete"},
			},
			// # Allow Dashboard to get and update 'kubernetes-dashboard-settings' config map.
			{
				APIGroups:     []string{""},
				Resources:     []string{"configmaps"},
				ResourceNames: []string{"kubernetes-dashboard-settings"},
				Verbs:         []string{"update"},
			},
			// # Allow Dashboard to get metrics from heapster.
			{
				APIGroups:     []string{""},
				Resources:     []string{"services"},
				ResourceNames: []string{"heapster"},
				Verbs:         []string{"proxy"},
			},
			{
				APIGroups:     []string{""},
				Resources:     []string{"services/proxy"},
				ResourceNames: []string{"heapster", "http:heapster:", "https:heapster:"},
				Verbs:         []string{"get"},
			},
		}

		clusterRole, err := k8sutil.GetOrCreateClusterRole(log, client, clusterRoleName, rules)
		if err != nil {
			return err
		}

		// cluster role binding
		clusterRoleBindingName := k8sDashboardReleaseName
		_, err = k8sutil.GetOrCreateClusterRoleBinding(log, client, clusterRoleBindingName, serviceAccount, clusterRole)
		if err != nil {
			return err
		}

		values := map[string]interface{}{
			"rbac": map[string]bool{
				"create":           false,
				"clusterAdminRole": false,
			},
			"serviceAccount": map[string]interface{}{
				"create": false,
				"name":   serviceAccount.Name,
			},
		}

		valuesJson, err = yaml.Marshal(values)
		if err != nil {
			return err
		}

	}

	return installDeployment(cluster, k8sDashboardNameSpace, pkgHelm.BanzaiRepository+"/kubernetes-dashboard", k8sDashboardReleaseName, valuesJson, "", false)

}

// InstallClusterAutoscalerPostHook post hook only for AWS & Azure for now
func InstallClusterAutoscalerPostHook(cluster CommonCluster) error {
	return DeployClusterAutoscaler(cluster)
}

func metricsServerIsInstalled(cluster CommonCluster) bool {
	kubeConfig, err := cluster.GetK8sConfig()
	if err != nil {
		log.Errorf("Getting cluster config failed: %s", err.Error())
		return false
	}

	client, err := k8sclient.NewClientFromKubeConfig(kubeConfig)
	if err != nil {
		log.Errorf("Getting K8s client failed: %s", err.Error())
		return false
	}

	serverGroups, err := client.ServerGroups()
	for _, group := range serverGroups.Groups {
		if group.Name == "metrics.k8s.io" {
			for _, v := range group.Versions {
				if v.GroupVersion == "metrics.k8s.io/v1beta1" {
					return true
				}
			}
		}
	}
	return false
}

// InstallHorizontalPodAutoscalerPostHook
func InstallHorizontalPodAutoscalerPostHook(cluster CommonCluster) error {
	promServiceName := global.Config.Cluster.Autoscale.HPA.Prometheus.ServiceName
	infraNamespace := global.Config.Cluster.Autoscale.Namespace
	serviceContext := global.Config.Cluster.Autoscale.HPA.Prometheus.ServiceContext
	chartName := global.Config.Cluster.Autoscale.Charts.HPAOperator.Chart
	chartVersion := global.Config.Cluster.Autoscale.Charts.HPAOperator.Version

	values := map[string]interface{}{
		"kube-metrics-adapter": map[string]interface{}{
			"prometheus": map[string]interface{}{
				"url": fmt.Sprintf("http://%s.%s.svc/%s", promServiceName, infraNamespace, serviceContext),
			},
		},
	}

	// install metricsServer for Amazon & Azure & Alibaba & Oracle only if metrics.k8s.io endpoint is not available already
	switch cluster.GetCloud() {
	case pkgCluster.Amazon, pkgCluster.Azure, pkgCluster.Alibaba, pkgCluster.Oracle:
		if !metricsServerIsInstalled(cluster) {
			log.Infof("Metrics Server is not installed, installing")
			values["metricsServer"] = map[string]interface{}{
				"enabled": true,
			}
			values["metrics-server"] = map[string]interface{}{
				"rbac": map[string]interface{}{"create": true},
			}
		} else {
			log.Infof("Metrics Server is already installed")
		}
	}

	valuesOverride, err := yaml.Marshal(values)
	if err != nil {
		return err
	}

	return installDeployment(cluster, infraNamespace, chartName,
		"hpa-operator", valuesOverride, chartVersion, false)
}

// InstallPVCOperatorPostHook installs the PVC operator
func InstallPVCOperatorPostHook(cluster CommonCluster) error {
	infraNamespace := global.Config.Cluster.Namespace

	values := map[string]interface{}{}
	valuesOverride, err := yaml.Marshal(values)
	if err != nil {
		return err
	}

	return installDeployment(cluster, infraNamespace, pkgHelm.BanzaiRepository+"/pvc-operator", "pvc-operator", valuesOverride, "", false)
}

func LabelKubeSystemNamespacePostHook(cluster CommonCluster) error {
	kubeConfig, err := cluster.GetK8sConfig()
	if err != nil {
		log.Errorf("Unable to fetch config for posthook: %s", err.Error())
		return err
	}

	client, err := k8sclient.NewClientFromKubeConfig(kubeConfig)
	if err != nil {
		log.Errorf("Could not get kubernetes client: %s", err)
		return err
	}

	return k8sutil.EnsureLabelsOnNamespace(client, k8sutil.KubeSystemNamespace, map[string]string{"name": k8sutil.KubeSystemNamespace})
}

// InstallHelmPostHook this posthook installs the helm related things
func InstallHelmPostHook(cluster CommonCluster) error {
	log := log.WithFields(logrus.Fields{"cluster": cluster.GetName(), "clusterID": cluster.GetID()})
	helmInstall := &pkgHelm.Install{
		Namespace:      k8sutil.KubeSystemNamespace,
		ServiceAccount: "tiller",
		ImageSpec:      fmt.Sprintf("gcr.io/kubernetes-helm/tiller:%s", global.Config.Helm.Tiller.Version),
		Upgrade:        true,
		ForceUpgrade:   true,
	}

	if cluster.GetDistribution() == pkgCluster.PKE {
		// add toleration for master node
		helmInstall.Tolerations = []v1.Toleration{
			{
				Key:      pke.TaintKeyMaster,
				Operator: v1.TolerationOpExists,
			},
		}

		// try to schedule to master or master-worker node
		helmInstall.NodeAffinity = &v1.NodeAffinity{
			PreferredDuringSchedulingIgnoredDuringExecution: []v1.PreferredSchedulingTerm{
				{
					Weight: 100,
					Preference: v1.NodeSelectorTerm{
						MatchExpressions: []v1.NodeSelectorRequirement{
							{
								Key:      pke.TaintKeyMaster,
								Operator: v1.NodeSelectorOpExists,
							},
						},
					},
				},
				{
					Weight: 100,
					Preference: v1.NodeSelectorTerm{
						MatchExpressions: []v1.NodeSelectorRequirement{
							{
								Key:      pke.NodeLabelKeyMasterWorker,
								Operator: v1.NodeSelectorOpExists,
							},
						},
					},
				},
			},
		}
	}

	kubeconfig, err := cluster.GetK8sConfig()
	if err != nil {
		return err
	}

	err = helm.RetryHelmInstall(log, helmInstall, kubeconfig)
	if err == nil {
		log.Info("Getting K8S Config Succeeded")

		if err := WaitingForTillerComeUp(log, kubeconfig); err != nil {
			return err
		}

	} else {
		log.Errorf("Error during retry helm install: %s", err.Error())
	}
	return nil
}

// LabelNodesWithNodePoolName add node pool name labels for all nodes.
// It's used only used in case of ACK etc. when we're not able to add labels via API.
func LabelNodesWithNodePoolName(commonCluster CommonCluster) error {

	switch commonCluster.GetDistribution() {
	case pkgCluster.EKS, pkgCluster.OKE, pkgCluster.GKE, pkgCluster.PKE:
		log.Infof("nodes are already labelled on : %v", commonCluster.GetDistribution())
		return nil
	}

	log.Debug("get K8S config")
	kubeConfig, err := commonCluster.GetK8sConfig()
	if err != nil {
		return err
	}

	log.Debug("get K8S connection")
	client, err := k8sclient.NewClientFromKubeConfig(kubeConfig)
	if err != nil {
		return err
	}

	log.Debug("list node names")
	nodeNames, err := commonCluster.ListNodeNames()
	if err != nil {
		return err
	}

	for poolName, nodes := range nodeNames {

		log.Debugf("nodepool: [%s]", poolName)
		for _, nodeName := range nodes {
			log.Infof("add label to node [%s]", nodeName)
			labels := map[string]string{pkgCommon.LabelKey: poolName}

			if err := addLabelsToNode(client, nodeName, labels); err != nil {
				log.Warnf("error during adding label to node [%s]: %s", nodeName, err.Error())
			}
		}
	}

	log.Info("add labels finished")

	return nil
}

// addLabelsToNode add label to the given node
func addLabelsToNode(client *kubernetes.Clientset, nodeName string, labels map[string]string) (err error) {

	tokens := make([]string, 0, len(labels))
	for k, v := range labels {
		tokens = append(tokens, "\""+k+"\":\""+v+"\"")
	}
	labelString := "{" + strings.Join(tokens, ",") + "}"
	patch := fmt.Sprintf(`{"metadata":{"labels":%v}}`, labelString)

	_, err = client.CoreV1().Nodes().Patch(nodeName, types.MergePatchType, []byte(patch))
	return
}

// RestoreFromBackup restores an ARK backup
func RestoreFromBackup(cluster CommonCluster, param pkgCluster.PostHookParam) error {

	var params arkAPI.RestoreFromBackupParams
	err := castToPostHookParam(&param, &params)
	if err != nil {
		return err
	}

	return arkPosthook.RestoreFromBackup(params, cluster, global.DB(), log, errorHandler, global.Config.Cluster.DisasterRecovery.Ark.RestoreWaitTimeout)
}

// InitSpotConfig creates a ConfigMap to store spot related config and installs the scheduler and the spot webhook charts
func InitSpotConfig(cluster CommonCluster) error {

	spot, err := isSpotCluster(cluster)
	if err != nil {
		return emperror.Wrap(err, "failed to check if cluster has spot instances")
	}

	if !spot {
		log.Debug("cluster doesn't have spot priced instances, spot post hook won't run")
		return nil
	}

	pipelineSystemNamespace := global.Config.Cluster.Namespace

	kubeConfig, err := cluster.GetK8sConfig()
	if err != nil {
		return emperror.Wrap(err, "failed to get Kubernetes config")
	}

	client, err := k8sclient.NewClientFromKubeConfig(kubeConfig)
	if err != nil {
		return emperror.Wrap(err, "failed to get Kubernetes clientset from kubeconfig")
	}

	err = initializeSpotConfigMap(client, pipelineSystemNamespace)
	if err != nil {
		return emperror.Wrap(err, "failed to initialize spot ConfigMap")
	}

	values := map[string]interface{}{}
	marshalledValues, err := yaml.Marshal(values)
	if err != nil {
		return emperror.Wrap(err, "failed to marshal yaml values")
	}

	err = installDeployment(cluster, pipelineSystemNamespace, pkgHelm.BanzaiRepository+"/spot-scheduler", "spot-scheduler", marshalledValues, "", false)
	if err != nil {
		return emperror.Wrap(err, "failed to install the spot-scheduler deployment")
	}
	err = installDeployment(cluster, pipelineSystemNamespace, pkgHelm.BanzaiRepository+"/spot-config-webhook", "spot-webhook", marshalledValues, "", true)
	if err != nil {
		return emperror.Wrap(err, "failed to install the spot-config-webhook deployment")
	}
	return nil
}

// DeployInstanceTerminationHandler deploys the instance termination handler
func DeployInstanceTerminationHandler(cluster CommonCluster) error {
	cloud := cluster.GetCloud()

	if cloud != pkgCluster.Amazon && cloud != pkgCluster.Google {
		return nil
	}

	pipelineSystemNamespace := global.Config.Cluster.Namespace

	values := map[string]interface{}{
		"tolerations": []v1.Toleration{
			{
				Operator: v1.TolerationOpExists,
			},
		},
		"hollowtreesNotifier": map[string]interface{}{
			"enabled": false,
		},
	}

	scaleOptions := cluster.GetScaleOptions()
	if scaleOptions != nil && scaleOptions.Enabled == true {
		tokenSigningKey := global.Config.Hollowtrees.TokenSigningKey
		if tokenSigningKey == "" {
			err := errors.New("no Hollowtrees token signkey specified")
			errorHandler.Handle(err)
			return err
		}

		generator := hollowtrees.NewTokenGenerator(
			global.Config.Auth.Token.Issuer,
			global.Config.Auth.Token.Audience,
			global.Config.Hollowtrees.TokenSigningKey,
		)
		_, token, err := generator.Generate(cluster.GetID(), cluster.GetOrganizationId(), nil)
		if err != nil {
			err = emperror.Wrap(err, "could not generate JWT token for instance termination handler")
			errorHandler.Handle(err)
			return err
		}

		values["hollowtreesNotifier"] = map[string]interface{}{
			"enabled":        true,
			"URL":            global.Config.Hollowtrees.Endpoint + "/alerts",
			"organizationID": cluster.GetOrganizationId(),
			"clusterID":      cluster.GetID(),
			"clusterName":    cluster.GetName(),
			"jwtToken":       token,
		}
	}

	marshalledValues, err := yaml.Marshal(values)
	if err != nil {
		return emperror.Wrap(err, "failed to marshal yaml values")
	}

	return installDeployment(cluster, pipelineSystemNamespace, pkgHelm.BanzaiRepository+"/instance-termination-handler", "ith", marshalledValues, "", false)
}

func isSpotCluster(cluster CommonCluster) (bool, error) {
	status, err := cluster.GetStatus()
	if err != nil {
		return false, emperror.Wrap(err, "failed to get cluster status")
	}
	return status.Spot, nil
}

func initializeSpotConfigMap(client *kubernetes.Clientset, systemNs string) error {
	log.Debug("initializing ConfigMap to store spot configuration")
	_, err := client.CoreV1().ConfigMaps(systemNs).Get(pkgCommon.SpotConfigMapKey, metav1.GetOptions{})
	if err != nil {
		if apiErrors.IsNotFound(err) {
			_, err = client.CoreV1().ConfigMaps(systemNs).Create(&v1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name: pkgCommon.SpotConfigMapKey,
				},
				Data: make(map[string]string),
			})
			if err != nil {
				return emperror.Wrap(err, "failed to create spot ConfigMap")
			}
		} else {
			return emperror.Wrap(err, "failed to retrieve spot ConfigMap")
		}
	}
	log.Info("finished initializing spot ConfigMap")
	return nil
}

// CreateClusterRoles creates the pre-defined ClusterRoles for a PKE cluster
func CreateClusterRoles(cluster CommonCluster) error {
	if distro := cluster.GetDistribution(); distro != pkgCluster.PKE {
		log.Infof("Not creating ClusterRoleBindings for %s", distro)
		return nil
	}

	kubeConfig, err := cluster.GetK8sConfig()
	if err != nil {
		return emperror.Wrap(err, "failed to get Kubernetes config")
	}

	client, err := k8sclient.NewClientFromKubeConfig(kubeConfig)
	if err != nil {
		return emperror.Wrap(err, "failed to get Kubernetes clientset from kubeconfig")
	}

	org, err := auth.GetOrganizationById(cluster.GetOrganizationId())
	if err != nil {
		return emperror.Wrap(err, "failed to get organization of Kubernetes cluster")
	}

	clusterRoleBinding := rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: org.Name + "-cluster-admin",
		},
		RoleRef: rbacv1.RoleRef{
			Kind: "ClusterRole",
			Name: "cluster-admin",
		},
		Subjects: []rbacv1.Subject{{
			Kind: rbacv1.GroupKind,
			Name: org.Name,
		}},
	}

	_, err = client.RbacV1().ClusterRoleBindings().Create(&clusterRoleBinding)

	if err != nil {
		return emperror.WrapWith(err, "failed to ClusterRoleBinding", "name", clusterRoleBinding.Name)
	}

	return nil
}
