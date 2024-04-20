/*
Copyright 2024.

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

package controller

import (
	"context"
	"fmt"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	webappv1 "lr97128.com/nginx-operator/api/v1"
)

// Definitions to manage status conditions
const (
	// typeAvailableMemcached represents the status of the Deployment reconciliation
	typeAvailableNginx = "Available"
	// typeDegradedMemcached represents the status used when the custom resource is deleted and the finalizer operations are yet to occur.
	// typeDegradedNginx = "Degraded"
	// appFinalizer      = "webapp.lr97128.com/finalizer"
	appPortName = "app"
)

// NginxReconciler reconciles a Nginx object
type NginxReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

//+kubebuilder:rbac:groups=webapp.lr97128.com,resources=nginxes,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=webapp.lr97128.com,resources=nginxes/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=webapp.lr97128.com,resources=nginxes/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Nginx object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.17.2/pkg/reconcile
func (r *NginxReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// TODO(user): your logic here
	//新建nginx资源对象
	app := &webappv1.Nginx{}

	if err := r.Get(ctx, req.NamespacedName, app); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("nginx_app resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get nginx_app")
		return ctrl.Result{}, err
	}

	deployment := &appsv1.Deployment{}
	//查询对应的deployment对象是否存在
	if err := r.Get(ctx, types.NamespacedName{Namespace: app.Namespace, Name: app.Name}, deployment); err != nil {
		//如果不存在，即err的类型是IsNotFound，就新增一个
		if apierrors.IsNotFound(err) {
			d, err := r.deploymentForNginx(app)
			if err != nil {
				logger.Error(err, "Failed to define new Deployment resource for Nginx")
				meta.SetStatusCondition(&app.Status.Conditions, metav1.Condition{Type: typeAvailableNginx,
					Status: metav1.ConditionFalse, Reason: "Reconciling",
					Message: fmt.Sprintf("Failed to create Deployment for the custom resource (%s): (%s)", app.Name, err)})

				if err := r.Status().Update(ctx, app); err != nil {
					logger.Error(err, "Failed to update Nginx status")
					return ctrl.Result{}, err
				}
				return ctrl.Result{}, err
			}
			logger.Info("Creating a new Deployment", "Deployment.Namespace", app.Namespace, "Deployment.Name", app.Name)
			if err := r.Create(ctx, d); err != nil {
				logger.Error(err, "Failed to create new Deployment", "Deployment.Namespace", d.Namespace, "Deployment.Name", d.Name)
				return ctrl.Result{}, err
			}
			// Deployment created successfully
			// We will requeue the reconciliation so that we can ensure the state
			// and move forward for the next operations
			return ctrl.Result{RequeueAfter: time.Minute}, nil
		} else {
			logger.Error(err, "Failed to get Deployment")
			// Let's return the error for the reconciliation be re-trigged again
			return ctrl.Result{}, err
		}
	} else {
		if err := r.Update(ctx, deployment); err != nil {
			return ctrl.Result{}, err
		}
	}
	//查询enableService是否为true，如果是true，就创建对应的service
	if app.Spec.EnableService {
		logger.Info("Someone set Spec.enableService to true, kubernetes cluster must have service resource for nginx")
		service := &corev1.Service{}
		//判断k8s中是否存在该service资源对象
		if err := r.Get(ctx, types.NamespacedName{Namespace: app.Namespace, Name: app.Name}, service); err != nil {
			if apierrors.IsNotFound(err) {
				logger.Info("Create new service resource for nginx")
				//新增该service资源对象
				s, err := r.serviceForNginx(app)
				if err != nil {
					logger.Error(err, "Failed to define new Service resource for Nginx")
					meta.SetStatusCondition(&app.Status.Conditions, metav1.Condition{Type: typeAvailableNginx,
						Status: metav1.ConditionFalse, Reason: "Reconciling",
						Message: fmt.Sprintf("Failed to create Service for the custom resource (%s): (%s)", app.Name, err)})

					if err := r.Status().Update(ctx, app); err != nil {
						logger.Error(err, "Failed to update Service status")
						return ctrl.Result{}, err
					}
					return ctrl.Result{}, err

				}
				logger.Info("Creating a new Service", "Service.Namespace", app.Namespace, "Service.Name", app.Name)
				if err := r.Create(ctx, s); err != nil {
					logger.Error(err, "Failed to create new Service", "Service.Namespace", app.Namespace, "Service.Name", app.Name)
					return ctrl.Result{}, err
				}
			} else {
				logger.Error(err, "Get some wrong when get Service for nginx")
				// Let's return the error for the reconciliation be re-trigged again
				return ctrl.Result{}, err
			}
		} else {
			logger.Info("There is service for nginx, do nothing")
		}

		//TODO Ingress
		if app.Spec.EnableIngress {
			//TODO ingress logic
			logger.Info("Yaml file set Spec.enableService and Spec.enableIngress to true,kubernetes cluster must have ingress resource for nginx")
			ingress := &netv1.Ingress{}
			if err := r.Get(ctx, types.NamespacedName{Namespace: app.Namespace, Name: app.Name}, ingress); err != nil {
				if apierrors.IsNotFound(err) {
					//enableIngress是true，而集群中没有该service资源对象，就要新增
					logger.Info("Create new service resource for nginx Service")
					i, err := r.ingressForNginx(app)
					if err != nil {
						logger.Error(err, "Failed to define new Ingress resource for Nginx Service")
						return ctrl.Result{}, err
					}
					logger.Info("Creating a new ingress", "ingress.Namespace", app.Namespace, "ingress.Name", app.Name)
					if err := r.Create(ctx, i); err != nil {
						logger.Error(err, "Failed to create new Ingress", "Ingress.Namespace", app.Namespace, "Ingress.Name", app.Name)
						// Let's return the error for the reconciliation be re-trigged again
						return ctrl.Result{}, err
					}
				} else {
					logger.Error(err, "Failed to get Ingress")
					return ctrl.Result{}, err
				}
			} else {
				//anbleIngress是true，而集群中没有该service资源对象，啥都不用干
				logger.Info("There is ingress for nginx. do nothing")
			}
		} else {
			//anbleIngress是false
			ingress := &netv1.Ingress{}
			if err := r.Get(ctx, types.NamespacedName{Namespace: app.Namespace, Name: app.Name}, ingress); err != nil {
				if !apierrors.IsNotFound(err) {
					logger.Error(err, "Failed to Get Ingress resource for Nginx Service")
					return ctrl.Result{}, err
				}
			} else {
				//集群中有对应的ingress资源对象，则需要删除它
				logger.Info("enableIngress set false, must delete ingress resource for Ngnix")
				if err := r.Delete(ctx, ingress); err != nil {
					logger.Error(err, "Failed to delete Ingress resource for Nginx")
					return ctrl.Result{}, err
				} else {
					logger.Info("Delete ingress resource for Nginx Successful")
				}
			}
		}
	} else {
		logger.Info("Yaml file set Spec.enableService to false,  must delete service resource for ngnix")
		service := &corev1.Service{}
		if err := r.Get(ctx, types.NamespacedName{Namespace: app.Namespace, Name: app.Name}, service); err != nil {
			if !apierrors.IsNotFound(err) {
				logger.Error(err, "Failed to Get service resource for nginx")
				return ctrl.Result{}, err
			} else {
				logger.Info("There is no service resource in kubernetes cluster. finish...")
			}
		} else {
			//集群中获取了service资源对象，需要删除它
			if err := r.Delete(ctx, service); err != nil {
				logger.Error(err, "Failed to delete service resource for Nginx")
				return ctrl.Result{}, err
			}
		}
		if app.Spec.EnableIngress {
			app.Spec.EnableIngress = false
			if err := r.Update(ctx, app); err != nil {
				logger.Error(err, "Failed to update nginx status")
				return ctrl.Result{}, err
			}
		} else {
			//anbleService和anbleIngress是false, 删除ingress
			ingress := &netv1.Ingress{}
			if err := r.Get(ctx, types.NamespacedName{Namespace: app.Namespace, Name: app.Name}, ingress); err != nil {
				if apierrors.IsNotFound(err) {
					logger.Info("There is no ingress resource in kubernetes cluster. only delete service resource for nginx")
				} else {
					logger.Error(err, "Failed to Get Ingress resource for Nginx")
					return ctrl.Result{}, err
				}
			} else {
				//集群中存在该ingress，则需要删除
				if err := r.Delete(ctx, ingress); err != nil {
					logger.Error(err, "Failed to delete Ingress resource for Nginx")
					return ctrl.Result{}, err
				}
			}
		}
	}
	return ctrl.Result{}, nil
}

func (r *NginxReconciler) serviceForNginx(app *webappv1.Nginx) (*corev1.Service, error) {
	servicePort := app.Spec.ServicePort
	containerPort := app.Spec.ContainerPort
	ls := r.getLabels(app)
	s := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      app.Name,
			Namespace: app.Namespace,
		},
		Spec: corev1.ServiceSpec{
			Selector: ls,
			Type:     corev1.ServiceTypeClusterIP,
			Ports: []corev1.ServicePort{
				{
					Name:       appPortName,
					Protocol:   "TCP",
					Port:       servicePort,
					TargetPort: intstr.FromInt32(containerPort),
				},
			},
		},
	}
	if err := ctrl.SetControllerReference(app, s, r.Scheme); err != nil {
		return nil, err
	}
	return s, nil
}

func (r *NginxReconciler) ingressForNginx(app *webappv1.Nginx) (*netv1.Ingress, error) {
	baseUrl := app.Spec.Baseurl
	path := app.Spec.Path
	// servicePort := app.Spec.ServicePort
	ingressClassName := &app.Spec.IngressesClass
	ingressPathType := netv1.PathTypePrefix
	ls := r.getLabels(app)
	i := &netv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      app.Name,
			Namespace: app.Namespace,
			Labels:    ls,
		},
		Spec: netv1.IngressSpec{
			IngressClassName: ingressClassName,
			Rules: []netv1.IngressRule{
				{
					Host: baseUrl,
					IngressRuleValue: netv1.IngressRuleValue{
						HTTP: &netv1.HTTPIngressRuleValue{
							Paths: []netv1.HTTPIngressPath{
								{
									Path:     path,
									PathType: &ingressPathType,
									Backend: netv1.IngressBackend{
										Service: &netv1.IngressServiceBackend{
											Name: app.Name,
											Port: netv1.ServiceBackendPort{
												Name: appPortName,
												// Number: 80,
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
	if err := ctrl.SetControllerReference(app, i, r.Scheme); err != nil {
		return nil, err
	}
	return i, nil
}

func (r *NginxReconciler) deploymentForNginx(app *webappv1.Nginx) (*appsv1.Deployment, error) {
	replicas := app.Spec.Replicas

	ls := r.getLabels(app)

	// Get the Operand image
	image := app.Spec.Image

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      app.Name,
			Namespace: app.Namespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: ls,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: ls,
				},
				Spec: corev1.PodSpec{
					// TODO(user): Uncomment the following code to configure the nodeAffinity expression
					// according to the platforms which are supported by your solution. It is considered
					// best practice to support multiple architectures. build your manager image using the
					// makefile target docker-buildx. Also, you can use docker manifest inspect <image>
					// to check what are the platforms supported.
					// More info: https://kubernetes.io/docs/concepts/scheduling-eviction/assign-pod-node/#node-affinity
					//Affinity: &corev1.Affinity{
					//	NodeAffinity: &corev1.NodeAffinity{
					//		RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
					//			NodeSelectorTerms: []corev1.NodeSelectorTerm{
					//				{
					//					MatchExpressions: []corev1.NodeSelectorRequirement{
					//						{
					//							Key:      "kubernetes.io/arch",
					//							Operator: "In",
					//							Values:   []string{"amd64", "arm64", "ppc64le", "s390x"},
					//						},
					//						{
					//							Key:      "kubernetes.io/os",
					//							Operator: "In",
					//							Values:   []string{"linux"},
					//						},
					//					},
					//				},
					//			},
					//		},
					//	},
					//},
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot: &[]bool{false}[0],
						// IMPORTANT: seccomProfile was introduced with Kubernetes 1.19
						// If you are looking for to produce solutions to be supported
						// on lower versions you must remove this option.
						SeccompProfile: &corev1.SeccompProfile{
							Type: corev1.SeccompProfileTypeRuntimeDefault,
						},
					},
					Containers: []corev1.Container{{
						Image:           image,
						Name:            "appnginx",
						ImagePullPolicy: corev1.PullIfNotPresent,
						// Ensure restrictive context for the container
						// More info: https://kubernetes.io/docs/concepts/security/pod-security-standards/#restricted
						// SecurityContext: &corev1.SecurityContext{
						// 	RunAsNonRoot:             &[]bool{false}[0],
						// 	AllowPrivilegeEscalation: &[]bool{true}[0],
						// 	Capabilities: &corev1.Capabilities{
						// 		Drop: []corev1.Capability{
						// 			"ALL",
						// 		},
						// 	},
						// },
						Ports: []corev1.ContainerPort{{
							ContainerPort: app.Spec.ContainerPort,
							Name:          "nginx",
						}},
					}},
				},
			},
		},
	}

	// Set the ownerRef for the Deployment
	// More info: https://kubernetes.io/docs/concepts/overview/working-with-objects/owners-dependents/
	if err := ctrl.SetControllerReference(app, dep, r.Scheme); err != nil {
		return nil, err
	}
	return dep, nil
}

func (r *NginxReconciler) getLabels(app *webappv1.Nginx) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       "app-operator",
		"app.kubernetes.io/version":    strings.Split(app.Spec.Image, ":")[1],
		"app.kubernetes.io/managed-by": "AppController",
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *NginxReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&webappv1.Nginx{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Owns(&netv1.Ingress{}).
		Complete(r)
}
