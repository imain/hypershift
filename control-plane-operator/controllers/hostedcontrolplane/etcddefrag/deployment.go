package etcddefrag

import (
	"context"

	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/util"
	utilpointer "k8s.io/utils/pointer"
)

const (
	configHashAnnotation = "etcddefrag.hypershift.openshift.io/config-hash"
)

var (
	volumeMounts = util.PodVolumeMounts{
		etcdDefragContainerMain().Name: {
			etcdDefragVolumeClientCert().Name: "/var/run/secrets/etcd-client",
			etcdDefragVolumeTrustedCA().Name:  "/var/run/configmaps/etcd-ca/ca.crt",
		},
	}
)

func etcdDefragLabels() map[string]string {
	return map[string]string{
		"app":                         "etcd-defrag-controller-openshift",
		hyperv1.ControlPlaneComponent: "etcd-defrag-controller-openshift",
	}
}

func ReconcileDeployment(ctx context.Context, deployment *appsv1.Deployment, image string, ownerRef config.OwnerRef) error {
	ownerRef.ApplyTo(deployment)

	/*
		mainContainer := util.FindContainer(etcdDefragContainerMain().Name, deployment.Spec.Template.Spec.Containers)
		if mainContainer != nil {
			deploymentConfig.SetContainerResourcesIfPresent(mainContainer)
		}
	*/
	if deployment.Spec.Selector == nil {
		deployment.Spec.Selector = &metav1.LabelSelector{
			MatchLabels: etcdDefragLabels(),
		}
	}
	deployment.Spec.Strategy.Type = appsv1.RollingUpdateDeploymentStrategyType
	maxSurge := intstr.FromInt(3)
	maxUnavailable := intstr.FromInt(1)
	deployment.Spec.Strategy.RollingUpdate = &appsv1.RollingUpdateDeployment{
		MaxSurge:       &maxSurge,
		MaxUnavailable: &maxUnavailable,
	}
	if deployment.Spec.Template.ObjectMeta.Labels == nil {
		deployment.Spec.Template.ObjectMeta.Labels = map[string]string{}
	}
	for k, v := range etcdDefragLabels() {
		deployment.Spec.Template.ObjectMeta.Labels[k] = v
	}
	if deployment.Spec.Template.ObjectMeta.Annotations == nil {
		deployment.Spec.Template.ObjectMeta.Annotations = map[string]string{}
	}
	deployment.Spec.Template.Spec = corev1.PodSpec{
		AutomountServiceAccountToken: utilpointer.Bool(false),
		Containers: []corev1.Container{
			util.BuildContainer(etcdDefragContainerMain(), buildEtcdDefragContainerMain(image)),
		},
		Volumes: []corev1.Volume{
			util.BuildVolume(etcdDefragVolumeClientCert(), buildEtcdDefragVolumeClientCert),
			util.BuildVolume(etcdDefragVolumeTrustedCA(), buildEtcdDefragVolumeTrustedCA),
		},
	}
	return nil
}

func etcdDefragContainerMain() *corev1.Container {
	return &corev1.Container{
		Name: "etcd-defrag-controller",
	}
}

func buildEtcdDefragContainerMain(image string) func(c *corev1.Container) {
	return func(c *corev1.Container) {
		c.Image = image
		c.Command = []string{"/usr/bin/control-plane-operator", "etcd-defrag-controller"}
		// TODO: Set cert locations?
		// c.Args =
		c.VolumeMounts = volumeMounts.ContainerMounts(c.Name)
		c.WorkingDir = volumeMounts.Path(c.Name, etcdDefragVolumeWorkLogs().Name)
		c.Env = []corev1.EnvVar{
			{
				Name:  "NO_PROXY",
				Value: manifests.KubeAPIServerService("").Name,
			},
		}
	}
}
func buildEtcdDefragVolumeClientCert(v *corev1.Volume) {
	v.ConfigMap = &corev1.ConfigMapVolumeSource{}
	v.ConfigMap.Name = manifests.EtcdClientSecret("").Name
}

func buildEtcdDefragVolumeTrustedCA(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{
		DefaultMode: utilpointer.Int32(0640),
		SecretName:  manifests.EtcdPeerSecret("").Name,
	}
}

func etcdDefragVolumeClientCert() *corev1.Volume {
	return &corev1.Volume{
		Name: "etcd-client-cert",
	}
}

func etcdDefragVolumeTrustedCA() *corev1.Volume {
	return &corev1.Volume{
		Name: "etcd-ca",
	}
}

func etcdDefragVolumeWorkLogs() *corev1.Volume {
	return &corev1.Volume{
		Name: "logs",
	}
}
