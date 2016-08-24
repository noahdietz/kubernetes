package isolation

import (
  "io"
  "fmt"
  "errors"

  clientset "k8s.io/kubernetes/pkg/client/clientset_generated/internalclientset"

  "k8s.io/kubernetes/pkg/admission"
  "k8s.io/kubernetes/pkg/api"
  k8sError "k8s.io/kubernetes/pkg/api/errors"
  "k8s.io/kubernetes/pkg/controller/framework"
	"k8s.io/kubernetes/pkg/controller/framework/informers"
)

const (
  // IngressAnnotationKey key for the network policy annotation in a namespace
  IngressAnnotationKey = "net.beta.kubernetes.io/network-policy"
  // IngressAnnotationValue is the policy that belongs to the NetworkPolicy key
  IngressAnnotationValue = "{\"ingress\": {\"isolation\": \"DefaultDeny\"}}"
  // NameLabelKey key for the name label used in network policy verification
  NameLabelKey = "Name"
)

func init() {
  admission.RegisterPlugin("NamespaceNetworkIsolation", func(client clientset.Interface, config io.Reader) (admission.Interface, error) {
    return NewIsolation(client), nil
  })
}

type isolation struct {
  *admission.Handler
  client             clientset.Interface
  namespaceInformer framework.SharedIndexInformer
}

var _ = admission.WantsInformerFactory(&isolation{})

func (i *isolation) Admit(a admission.Attributes) (err error) {
  // only looking for *new namespaces* to edit
  if a.GetKind().GroupKind() != api.Kind("Namespace") || a.GetOperation() != admission.Create || a.GetOperation() != admission.Update {
    return nil
  }

  namespaceObj := a.GetObject().(*api.Namespace)
  addIngressAnnotation(namespaceObj)
  addNameLabel(namespaceObj)

  i.namespaceInformer.GetStore().Update(namespaceObj)

  // verify that our update worked
  obj, _, err := i.namespaceInformer.GetStore().Get(namespaceObj)
  if err != nil {
    return k8sError.NewInternalError(err)
  }

  checkObj := obj.(*api.Namespace)
  checkAnnotations := checkObj.GetAnnotations()
  if checkAnnotations == nil { // there are no annotations in updated object
    return k8sError.NewInternalError(errors.New("Failed to add ingress isolation annotation"))
  } else if val, exists := checkAnnotations[IngressAnnotationKey]; !exists ||
    val != IngressAnnotationValue { // we didn't save the updated annotation properly
    return k8sError.NewInternalError(errors.New("Failed to update ingress isolation annotation properly"))
  }

  // annotation added
  return nil
}

// NewIsolation ensures that a newly created namespace is annotated with complete network isolation
func NewIsolation(c clientset.Interface) admission.Interface {
  return &isolation{
    client:  c,
    Handler: admission.NewHandler(admission.Create, admission.Update, admission.Delete),
  }
}

func addIngressAnnotation(namespaceObj *api.Namespace) {
  annotations := namespaceObj.GetAnnotations()

  if annotations == nil { // doesn't have any annotations, initialize them
    annotations = map[string]string{}
  } else if val, exists := annotations[IngressAnnotationKey]; exists &&
    val == IngressAnnotationValue { // has the ingress annotation & its what we want, we're done
    return
  }

  // doesn't have the annotation, so we add it
  annotations[IngressAnnotationKey] = IngressAnnotationValue
  namespaceObj.SetAnnotations(annotations)
}

func addNameLabel(namespaceObj *api.Namespace) {
  labels := namespaceObj.GetLabels()
  if labels == nil { // no labels
    labels = map[string]string{}
  } else if _, exists := labels[NameLabelKey]; exists {
    return // label already exists
  }

  // add `name` label with this namespace's name
  labels[NameLabelKey] = namespaceObj.Name
  namespaceObj.SetLabels(labels)
}

func (i *isolation) SetInformerFactory(f informers.SharedInformerFactory) {
	i.namespaceInformer = f.Namespaces().Informer()
	i.SetReadyFunc(i.namespaceInformer.HasSynced)
}

func (i *isolation) Validate() error {
	if i.namespaceInformer == nil {
		return fmt.Errorf("missing namespaceInformer")
	}
	return nil
}
