package main

import (
	"crypto/tls"
	"encoding/json"
	"flag"
	"io/ioutil"
	"log"
	"net/http"

	"k8s.io/api/admission/v1beta1"
	admissionregistrationv1beta1 "k8s.io/api/admissionregistration/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
)

type operation struct {
	Op    string      `json:"op"`
	Path  string      `json:"path"`
	Value interface{} `json:"value"`
}

var scheme = runtime.NewScheme()
var codecs = serializer.NewCodecFactory(scheme)
var labelName = "name"

func init() {
	corev1.AddToScheme(scheme)
	admissionregistrationv1beta1.AddToScheme(scheme)
}

func main() {
	var CertFile string
	var KeyFile string

	flag.StringVar(&CertFile, "tls-cert-file", CertFile, ""+
		"File containing the default x509 Certificate for HTTPS. (CA cert, if any, concatenated "+
		"after server cert).")
	flag.StringVar(&KeyFile, "tls-key-file", KeyFile, ""+
		"File containing the default x509 private key matching --tls-cert-file.")
	flag.StringVar(&labelName, "label-name", labelName, ""+
		"Label to set on namespace.")

	flag.Parse()

	http.HandleFunc("/", mkServe())
	server := &http.Server{
		Addr:      ":443",
		TLSConfig: configTLS(CertFile, KeyFile),
	}
	server.ListenAndServeTLS("", "")

}

func configTLS(CertFile string, KeyFile string) *tls.Config {
	sCert, err := tls.LoadX509KeyPair(CertFile, KeyFile)
	if err != nil {
		log.Fatal(err)
	}
	return &tls.Config{
		Certificates: []tls.Certificate{sCert},
	}
}

func mkServe() func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		var body []byte
		if r.Body != nil {
			if data, err := ioutil.ReadAll(r.Body); err == nil {
				body = data
			}
		}

		// verify the content type is accurate
		contentType := r.Header.Get("Content-Type")
		if contentType != "application/json" {
			log.Printf("contentType=%s, expect application/json", contentType)
			return
		}

		var reviewResponse *v1beta1.AdmissionResponse
		ar := v1beta1.AdmissionReview{}
		deserializer := codecs.UniversalDeserializer()
		if _, _, err := deserializer.Decode(body, nil, &ar); err != nil {
			log.Print(err)
			reviewResponse = toAdmissionResponse(err)
		} else {
			reviewResponse = admit(ar)
		}

		response := v1beta1.AdmissionReview{}
		if reviewResponse != nil {
			response.Response = reviewResponse
			response.Response.UID = ar.Request.UID
		}
		// reset the Object and OldObject, they are not needed in a response.
		ar.Request.Object = runtime.RawExtension{}
		ar.Request.OldObject = runtime.RawExtension{}

		resp, err := json.Marshal(response)
		if err != nil {
			log.Print(err)
		}
		if _, err := w.Write(resp); err != nil {
			log.Print(err)
		}
	}
}

func toAdmissionResponse(err error) *v1beta1.AdmissionResponse {
	return &v1beta1.AdmissionResponse{
		Result: &metav1.Status{
			Message: err.Error(),
		},
	}
}

func admit(ar v1beta1.AdmissionReview) *v1beta1.AdmissionResponse {
	namespaceResource := metav1.GroupVersionResource{Group: "", Version: "v1", Resource: "namespaces"}
	if ar.Request.Resource != namespaceResource {
		log.Printf("expected resource to be %s", namespaceResource)
		return nil
	}

	if ar.Request.Operation != "CREATE" && ar.Request.Operation != "UPDATE" {
		log.Print("expected operation to be CREATE or UPDATE")
		return nil
	}

	raw := ar.Request.Object.Raw
	namespace := corev1.Namespace{}
	deserializer := codecs.UniversalDeserializer()
	if _, _, err := deserializer.Decode(raw, nil, &namespace); err != nil {
		log.Print(err)
		return toAdmissionResponse(err)
	}

	reviewResponse := v1beta1.AdmissionResponse{}
	reviewResponse.Allowed = true

	operations := makePatch(&namespace)
	if len(operations) != 0 {
		patch, err := json.Marshal(operations)
		if err != nil {
			log.Print(err)
			return toAdmissionResponse(err)
		}

		reviewResponse.Patch = patch
		pt := v1beta1.PatchTypeJSONPatch
		reviewResponse.PatchType = &pt
	}

	return &reviewResponse
}

func makePatch(namespace *corev1.Namespace) []*operation {
	return []*operation{makeOperation(namespace)}
}

func makeOperation(namespace *corev1.Namespace) *operation {
	if len(namespace.Labels) == 0 {
		return &operation{
			Op:    "add",
			Path:  "/metadata/labels",
			Value: map[string]string{labelName: namespace.Name},
		}
	} else {
		return &operation{
			Op:    "add",
			Path:  "/metadata/labels/" + labelName,
			Value: namespace.Name,
		}
	}
}
