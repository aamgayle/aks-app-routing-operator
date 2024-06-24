package manifests

import (
	_ "embed"
	"github.com/Azure/aks-app-routing-operator/api/v1alpha1"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

//go:embed embedded/defaultBackendClient.go
var dbeClientContents string

//go:embed embedded/defaultBackendServer.go
var dbeServerContents string

type DefaultBackendResources struct {
	Client                 *appsv1.Deployment
	Server                 *appsv1.Deployment
	Ingress                *netv1.Ingress
	DefaultBackendServer   *appsv1.Deployment
	Service                *corev1.Service
	DefaultBackendService  *corev1.Service
	NginxIngressController *v1alpha1.NginxIngressController
}

func (t DefaultBackendResources) Objects() []client.Object {
	ret := []client.Object{
		t.Client,
		t.Server,
		t.Service,
		t.Ingress,
		t.DefaultBackendServer,
		t.DefaultBackendService,
		t.NginxIngressController,
	}

	for _, obj := range ret {
		setGroupKindVersion(obj)
	}

	return ret
}

func DefaultBackendClientAndServer(namespace, name, nameserver, keyvaultURI, host, tlsHost string) DefaultBackendResources {
	name = nonAlphanumericRegex.ReplaceAllString(name, "")

	// Client deployment
	clientDeployment := newGoDeployment(dbeClientContents, namespace, name+"-client")
	clientDeployment.Spec.Template.Annotations["openservicemesh.io/sidecar-injection"] = "disabled"
	clientDeployment.Spec.Template.Spec.Containers[0].Env = []corev1.EnvVar{
		{
			Name:  "URL",
			Value: "https://" + host + "/test",
		},
		{
			Name:  "TEST_URL",
			Value: "https://" + host + "/fakehost",
		},
		{
			Name:  "NAMESERVER",
			Value: nameserver,
		},
		{
			Name:      "POD_IP",
			ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: "status.podIP"}},
		},
	}
	clientDeployment.Spec.Template.Spec.Containers[0].ReadinessProbe = &corev1.Probe{
		FailureThreshold:    1,
		InitialDelaySeconds: 1,
		PeriodSeconds:       1,
		SuccessThreshold:    1,
		TimeoutSeconds:      5,
		ProbeHandler: corev1.ProbeHandler{
			HTTPGet: &corev1.HTTPGetAction{
				Path:   "/",
				Port:   intstr.FromInt(8080),
				Scheme: corev1.URISchemeHTTP,
			},
		},
	}

	// Main server deployment
	serverDeployment := newGoDeployment(serverContents, namespace, name)
	ingressName := name + "-ingress"

	service :=
		&corev1.Service{
			TypeMeta: metav1.TypeMeta{
				Kind:       "Service",
				APIVersion: "v1",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
				Annotations: map[string]string{
					ManagedByKey: ManagedByVal,
				},
			},
			Spec: corev1.ServiceSpec{
				Ports: []corev1.ServicePort{{
					Name:       "http",
					Port:       8080,
					TargetPort: intstr.FromInt(8080),
				}},
				Selector: map[string]string{
					"app": name,
				},
			},
		}

	// Default server deployment
	defaultServerName := "default-" + name + "-server"
	defaultServerDeployment := newGoDeployment(dbeServerContents, namespace, defaultServerName)
	defaultServiceName := "default-" + name
	ingressClassName := name + ".backend.ingressclass"
	dbeService :=
		&corev1.Service{
			TypeMeta: metav1.TypeMeta{
				Kind:       "Service",
				APIVersion: "v1",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      defaultServiceName,
				Namespace: namespace,
				Annotations: map[string]string{
					ManagedByKey: ManagedByVal,
				},
			},
			Spec: corev1.ServiceSpec{
				Ports: []corev1.ServicePort{{
					Name:       "http",
					Port:       8080,
					TargetPort: intstr.FromInt(8080),
				}},
				Selector: map[string]string{
					"app": defaultServerName,
				},
			},
		}

	ingress := &netv1.Ingress{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Ingress",
			APIVersion: "networking.k8s.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      ingressName,
			Namespace: namespace,
			Annotations: map[string]string{
				ManagedByKey: ManagedByVal,
				"kubernetes.azure.com/tls-cert-keyvault-uri": keyvaultURI,
			},
		},
		Spec: netv1.IngressSpec{
			IngressClassName: to.Ptr(ingressClassName),
			Rules: []netv1.IngressRule{{
				Host: host,
				IngressRuleValue: netv1.IngressRuleValue{
					HTTP: &netv1.HTTPIngressRuleValue{
						Paths: []netv1.HTTPIngressPath{{
							Path:     "/test",
							PathType: to.Ptr(netv1.PathTypePrefix),
							Backend: netv1.IngressBackend{
								Service: &netv1.IngressServiceBackend{
									Name: name,
									Port: netv1.ServiceBackendPort{
										Number: 8080,
									},
								},
							},
						}},
					},
				},
			}},
			TLS: []netv1.IngressTLS{{
				Hosts:      []string{tlsHost},
				SecretName: "keyvault-" + ingressName,
			}},
		},
	}

	if tlsHost == "" {
		ingress.Spec.Rules[0].Host = ""
		ingress.Spec.TLS = nil
		delete(ingress.Annotations, "kubernetes.azure.com/tls-cert-keyvault-uri")
	}

	nicName := name + "-dbe-nginxingress"

	defaultSSLCert := &v1alpha1.DefaultSSLCertificate{KeyVaultURI: &keyvaultURI}
	defaultBackendService := &v1alpha1.NICNamespacedName{defaultServiceName, namespace}

	nic := &v1alpha1.NginxIngressController{
		TypeMeta: metav1.TypeMeta{
			Kind:       "NginxIngressController",
			APIVersion: "approuting.kubernetes.azure.com/v1alpha1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      nicName,
			Namespace: namespace,
			Annotations: map[string]string{
				ManagedByKey: ManagedByVal,
			},
		},
		Spec: v1alpha1.NginxIngressControllerSpec{
			IngressClassName:      ingressClassName,
			ControllerNamePrefix:  "nginx-" + name[len(name)-7:],
			DefaultSSLCertificate: defaultSSLCert,
			DefaultBackendService: defaultBackendService,
		},
	}

	return DefaultBackendResources{
		Client:                 clientDeployment,
		Server:                 serverDeployment,
		Ingress:                ingress,
		DefaultBackendServer:   defaultServerDeployment,
		Service:                service,
		DefaultBackendService:  dbeService,
		NginxIngressController: nic,
	}
}