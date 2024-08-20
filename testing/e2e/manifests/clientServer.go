package manifests

import (
	_ "embed"
	"github.com/Azure/aks-app-routing-operator/api/v1alpha1"
	"regexp"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

//go:embed embedded/client.go
var clientContents string

//go:embed embedded/server.go
var serverContents string

var nonAlphanumericRegex = regexp.MustCompile(`[^a-zA-Z0-9 ]+`)

type ClientServerResources struct {
	Client       *appsv1.Deployment
	Server       *appsv1.Deployment
	Ingress      *netv1.Ingress
	Service      *corev1.Service
	AddedObjects []client.Object
}

type MainDeployments struct {
	Client  *appsv1.Deployment
	Server  *appsv1.Deployment
	Ingress *netv1.Ingress
	Service *corev1.Service
}

func (t ClientServerResources) Objects() []client.Object {
	ret := []client.Object{
		t.Client,
		t.Server,
		t.Service,
		t.Ingress,
	}

	ret = append(ret, t.AddedObjects...)

	for _, obj := range ret {
		setGroupKindVersion(obj)
	}

	return ret
}

func ClientAndServer(namespace, name, nameserver, keyvaultURI, host, tlsHost string) ClientServerResources {
	name = nonAlphanumericRegex.ReplaceAllString(name, "")

	objs := ClientAndServerObjs(namespace, name, nameserver, keyvaultURI, host, tlsHost)
	retReources := ClientServerResources{
		Client:  objs.Client,
		Server:  objs.Server,
		Ingress: objs.Ingress,
		Service: objs.Service,
	}

	if tlsHost == "" {
		objs.Ingress.Spec.Rules[0].Host = ""
		objs.Ingress.Spec.TLS = nil
		delete(objs.Ingress.Annotations, "kubernetes.azure.com/tls-cert-keyvault-uri")
	}

	return retReources
}

func ClientAndServerObjs(namespace, name, nameserver, keyvaultURI, host, tlsHost string) MainDeployments {
	clientDeployment := newGoDeployment(clientContents, namespace, name+"-client")
	clientDeployment.Spec.Template.Annotations["openservicemesh.io/sidecar-injection"] = "disabled"
	clientDeployment.Spec.Template.Spec.Containers[0].Env = []corev1.EnvVar{
		{
			Name:  "URL",
			Value: "https://" + host,
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

	serverName := name + "-server"
	serverDeployment := newGoDeployment(serverContents, namespace, serverName)
	serviceName := name + "-service"
	ingressName := name + "-ingress"

	service :=
		&corev1.Service{
			TypeMeta: metav1.TypeMeta{
				Kind:       "Service",
				APIVersion: "v1",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      serviceName,
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
					"app": serverName,
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
			IngressClassName: to.Ptr("webapprouting.kubernetes.azure.com"),
			Rules: []netv1.IngressRule{{
				Host: host,
				IngressRuleValue: netv1.IngressRuleValue{
					HTTP: &netv1.HTTPIngressRuleValue{
						Paths: []netv1.HTTPIngressPath{{
							Path:     "/",
							PathType: to.Ptr(netv1.PathTypePrefix),
							Backend: netv1.IngressBackend{
								Service: &netv1.IngressServiceBackend{
									Name: serviceName,
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

	return MainDeployments{
		Client:  clientDeployment,
		Server:  serverDeployment,
		Ingress: ingress,
		Service: service,
	}
}

//func DefaultBackendClientAndServerObjs() MainDeployments {
//	return MainDeployments{
//		Client:  {},
//		Server:  {},
//		Ingress: {},
//		Service: {},
//	}
//}

type TestValues struct {
	TestTypePrefix        string
	ClientContents        string
	ClientEnvironmentVars []corev1.EnvVar
	ServerContents        string
	ServicePort           int
	IngressPaths          []netv1.HTTPIngressPath
}

func ClientAndServerTestValues(nic v1alpha1.NginxIngressController) TestValues {
	testVals := TestValues{
		TestTypePrefix:        "",
		ClientContents:        "",
		ClientEnvironmentVars: []corev1.EnvVar{},
		ServerContents:        "",
		ServicePort:           8080,
		IngressPaths:          []netv1.HTTPIngressPath{},
	}

	// Client
	if nic.Spec.DefaultBackendService != nil {
		if nic.Spec.CustomHTTPErrors != nil {
			testVals.ClientContents = ceClientContents
			testVals.ClientEnvironmentVars = CustomErrorsEnv
		} else {
			testVals.ClientContents = dbClientContents

		}
	}
	// Ingress
	if nic.Spec.CustomHTTPErrors != nil {
		testVals.IngressPaths = append(
			testVals.IngressPaths,
			[]netv1.HTTPIngressPath{
				getIngressPath(liveServicePath, "live-service", 5678),
				getIngressPath(deadServicePath, "dead-service", 8080)}...)
	} else {
		testVals.IngressPaths = append(
			testVals.IngressPaths, getIngressPath(liveServicePath, "live-service", 8080))
	}

	return testVals
}

func getIngressPath(path, serviceName string, servicePort int32) netv1.HTTPIngressPath {
	return netv1.HTTPIngressPath{
		Path:     path,
		PathType: to.Ptr(netv1.PathTypePrefix),
		Backend: netv1.IngressBackend{
			Service: &netv1.IngressServiceBackend{
				Name: serviceName,
				Port: netv1.ServiceBackendPort{
					Number: servicePort,
				},
			},
		},
	}
}
func getEnvVar(name, value string) corev1.EnvVar {
	return corev1.EnvVar{
		Name:  name,
		Value: value,
	}
}
