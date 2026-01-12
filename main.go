package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"os"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

const (
	roleArnAnnotation         = "api.vultr.com/role"
	tokenVolumeName           = "vultr-irsa-token"
	tokenMountPath            = "/var/run/secrets/vultr.com/serviceaccount"
	tokenFileName             = "token"
	tokenAudience             = "vultr"
	envAWSRoleArn             = "AWS_ROLE_ARN"
	envAWSWebIdentityToken    = "AWS_WEB_IDENTITY_TOKEN_FILE"
	envAWSSTSRegionalEndpoint = "AWS_STS_REGIONAL_ENDPOINTS"
	envAWSEndpointUrlSTS      = "AWS_ENDPOINT_URL_STS"
)

var (
	tokenExpirationSeconds = int64(86400) // 24 hours
)

type WebhookServer struct {
	client *kubernetes.Clientset
}

// JSONPatch represents a JSON Patch operation
type JSONPatch struct {
	Op    string      `json:"op"`
	Path  string      `json:"path"`
	Value interface{} `json:"value,omitempty"`
}

type LogLevel int

const (
	LogInfo LogLevel = iota
	LogDebug
)

var currentLogLevel = LogInfo

func logInfo(format string, v ...interface{}) {
	log.Printf("[INFO] "+format, v...)
}

func logDebug(format string, v ...interface{}) {
	if currentLogLevel >= LogDebug {
		log.Printf("[DEBUG] "+format, v...)
	}
}

const alphaNum = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

func randomAlphaNum(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = alphaNum[rand.Intn(len(alphaNum))]
	}
	return string(b)
}

func main() {
	switch os.Getenv("LOG_LEVEL") {
	case "debug":
		currentLogLevel = LogDebug
	default:
		currentLogLevel = LogInfo
	}

	// Create in-cluster Kubernetes client
	config, err := rest.InClusterConfig()
	if err != nil {
		log.Fatalf("Failed to create in-cluster config: %v", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		log.Fatalf("Failed to create Kubernetes client: %v", err)
	}

	server := &WebhookServer{
		client: clientset,
	}

	// Setup HTTP handlers
	mux := http.NewServeMux()
	mux.HandleFunc("/mutate", server.handleMutate)
	mux.HandleFunc("/health", handleHealth)

	// TLS configuration
	tlsCertPath := getEnv("TLS_CERT_PATH", "/etc/webhook/certs/tls.crt")
	tlsKeyPath := getEnv("TLS_KEY_PATH", "/etc/webhook/certs/tls.key")
	port := getEnv("PORT", "8443")

	// Load TLS certificates
	cert, err := tls.LoadX509KeyPair(tlsCertPath, tlsKeyPath)
	if err != nil {
		log.Fatalf("Failed to load TLS certificates: %v", err)
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}

	httpServer := &http.Server{
		Addr:      fmt.Sprintf(":%s", port),
		TLSConfig: tlsConfig,
		Handler:   mux,
	}

	logInfo("Starting webhook server on port %s", port)
	if err := httpServer.ListenAndServeTLS("", ""); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}

func (ws *WebhookServer) handleMutate(w http.ResponseWriter, r *http.Request) {
	handleMutateReqId := fmt.Sprintf("[handleMutate Request-Id %s]:", randomAlphaNum(6))

	logDebug("%s New Request: method=%s path=%s content-length=%d", handleMutateReqId, r.Method, r.URL.Path, r.ContentLength)

	if r.Method != http.MethodPost {
		logDebug("%s HTTP Method != POST, returning %d Method Not Allowed", handleMutateReqId, http.StatusMethodNotAllowed)
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Read request body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		logDebug("%s Failed to read request body: %v", handleMutateReqId, err)
		http.Error(w, "Failed to read request", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// Parse AdmissionReview request
	admissionReview := &admissionv1.AdmissionReview{}
	if err := json.Unmarshal(body, admissionReview); err != nil {
		logDebug("%s JSON parsing error: %v", handleMutateReqId, err)
		http.Error(w, "Failed to parse admission review", http.StatusBadRequest)
		return
	}

	// Validate request
	if admissionReview.Request == nil {
		logDebug("%s Admission review request is nil, returning %d Bad Request", handleMutateReqId, http.StatusBadRequest)
		http.Error(w, "Invalid admission review request", http.StatusBadRequest)
		return
	}

	// Process the request and create response
	admissionResponse := ws.mutate(admissionReview.Request)

	// Create response AdmissionReview
	responseAdmissionReview := &admissionv1.AdmissionReview{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "admission.k8s.io/v1",
			Kind:       "AdmissionReview",
		},
		Response: admissionResponse,
	}
	responseAdmissionReview.Response.UID = admissionReview.Request.UID

	// Marshal and send response
	responseBytes, err := json.Marshal(responseAdmissionReview)
	if err != nil {
		logDebug("%s Failed to marshal admission response: %v", handleMutateReqId, err)
		http.Error(w, "Failed to create response", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(responseBytes)

	logDebug("%s Successfully Returned AdmissionReview Response", handleMutateReqId)
}

func (ws *WebhookServer) mutate(request *admissionv1.AdmissionRequest) *admissionv1.AdmissionResponse {
	mutateReqId := fmt.Sprintf("[mutate Request-Id %s]:", randomAlphaNum(6))

	// Create base response
	response := &admissionv1.AdmissionResponse{
		Allowed: true,
	}

	// Only handle Pod objects
	if request.Kind.Kind != "Pod" {
		log.Printf("Skipping non-Pod object: %s", request.Kind.Kind)
		return response
	}

	// Parse Pod from request
	pod := &corev1.Pod{}
	if err := json.Unmarshal(request.Object.Raw, pod); err != nil {
		logDebug("%s Failed to unmarshal pod: %v", mutateReqId, err)
		response.Allowed = false
		response.Result = &metav1.Status{
			Message: fmt.Sprintf("Failed to parse pod: %v", err),
		}
		return response
	}

	logDebug("%s Processing pod: %s/%s, ServiceAccount: %s", mutateReqId, request.Namespace, pod.Name, pod.Spec.ServiceAccountName)

	// Get ServiceAccount name (default to "default" if not specified)
	serviceAccountName := pod.Spec.ServiceAccountName
	if serviceAccountName == "" {
		serviceAccountName = "default"
	}

	// Fetch ServiceAccount from Kubernetes API
	serviceAccount, err := ws.client.CoreV1().ServiceAccounts(request.Namespace).Get(
		context.Background(),
		serviceAccountName,
		metav1.GetOptions{},
	)
	if err != nil {
		logDebug("%s Failed to fetch ServiceAccount %s/%s: %v", mutateReqId, request.Namespace, serviceAccountName, err)
		// Don't block pod creation if we can't fetch the SA
		return response
	}

	// Check for role ARN annotation
	roleArn, exists := serviceAccount.Annotations[roleArnAnnotation]
	if !exists || roleArn == "" {
		logDebug("%s ServiceAccount %s/%s does not have %s annotation, skipping mutation", mutateReqId, request.Namespace, serviceAccountName, roleArnAnnotation)
		return response
	}

	logDebug("%s Found role ARN annotation: %s", mutateReqId, roleArn)

	// Generate JSON patches to mutate the pod
	patches, err := ws.generatePatches(pod, roleArn)
	if err != nil {
		logDebug("%s Failed to generate patches: %v", mutateReqId, err)
		response.Allowed = false
		response.Result = &metav1.Status{
			Message: fmt.Sprintf("Failed to generate patches: %v", err),
		}
		return response
	}

	// Marshal patches to JSON
	patchBytes, err := json.Marshal(patches)
	if err != nil {
		logDebug("%s Failed to marshal patches: %v", mutateReqId, err)
		response.Allowed = false
		response.Result = &metav1.Status{
			Message: fmt.Sprintf("Failed to marshal patches: %v", err),
		}
		return response
	}

	response.Patch = patchBytes
	patchType := admissionv1.PatchTypeJSONPatch
	response.PatchType = &patchType

	logInfo("%s Successfully patched pod %s/%s for service account '%s'", mutateReqId, request.Namespace, pod.Name, serviceAccountName)

	return response
}

func (ws *WebhookServer) generatePatches(pod *corev1.Pod, roleArn string) ([]JSONPatch, error) {
	var patches []JSONPatch

	// 1. Add projected volume for service account token
	tokenVolume := corev1.Volume{
		Name: tokenVolumeName,
		VolumeSource: corev1.VolumeSource{
			Projected: &corev1.ProjectedVolumeSource{
				Sources: []corev1.VolumeProjection{
					{
						ServiceAccountToken: &corev1.ServiceAccountTokenProjection{
							Audience:          tokenAudience,
							ExpirationSeconds: &tokenExpirationSeconds,
							Path:              tokenFileName,
						},
					},
				},
			},
		},
	}

	// Check if volumes array exists
	if pod.Spec.Volumes == nil {
		// Initialize volumes array
		patches = append(patches, JSONPatch{
			Op:    "add",
			Path:  "/spec/volumes",
			Value: []corev1.Volume{tokenVolume},
		})
	} else {
		// Append to existing volumes
		patches = append(patches, JSONPatch{
			Op:    "add",
			Path:  "/spec/volumes/-",
			Value: tokenVolume,
		})
	}

	// 2. Process each container
	for i := range pod.Spec.Containers {
		containerPatches := ws.generateContainerPatches(i, roleArn, &pod.Spec.Containers[i])
		patches = append(patches, containerPatches...)
	}

	// 3. Process init containers if they exist
	for i := range pod.Spec.InitContainers {
		containerPatches := ws.generateContainerPatches(i, roleArn, &pod.Spec.InitContainers[i])
		// Adjust paths for init containers
		for j := range containerPatches {
			containerPatches[j].Path = "/spec/initContainers" +
				containerPatches[j].Path[len("/spec/containers"):]
		}
		patches = append(patches, containerPatches...)
	}

	return patches, nil
}

func (ws *WebhookServer) generateContainerPatches(index int, roleArn string, container *corev1.Container) []JSONPatch {
	var patches []JSONPatch
	basePath := fmt.Sprintf("/spec/containers/%d", index)

	// Add volume mount
	volumeMount := corev1.VolumeMount{
		Name:      tokenVolumeName,
		MountPath: tokenMountPath,
		ReadOnly:  true,
	}

	if container.VolumeMounts == nil {
		// Initialize volume mounts array
		patches = append(patches, JSONPatch{
			Op:    "add",
			Path:  basePath + "/volumeMounts",
			Value: []corev1.VolumeMount{volumeMount},
		})
	} else {
		// Append to existing volume mounts
		patches = append(patches, JSONPatch{
			Op:    "add",
			Path:  basePath + "/volumeMounts/-",
			Value: volumeMount,
		})
	}

	// Get STS endpoint from environment (set in deployment)
	stsEndpoint := getEnv("STS_ENDPOINT", "https://api.vultr.com/v2/assumed-roles/compatibility/aws/sts")

	// Add environment variables
	envVars := []corev1.EnvVar{
		{
			Name:  envAWSRoleArn,
			Value: roleArn,
		},
		{
			Name:  envAWSWebIdentityToken,
			Value: fmt.Sprintf("%s/%s", tokenMountPath, tokenFileName),
		},
		{
			Name:  envAWSSTSRegionalEndpoint,
			Value: "regional",
		},
		{
			Name:  envAWSEndpointUrlSTS,
			Value: stsEndpoint,
		},
	}

	if container.Env == nil {
		// Initialize env array
		patches = append(patches, JSONPatch{
			Op:    "add",
			Path:  basePath + "/env",
			Value: envVars,
		})
	} else {
		// Append to existing env vars
		for _, envVar := range envVars {
			patches = append(patches, JSONPatch{
				Op:    "add",
				Path:  basePath + "/env/-",
				Value: envVar,
			})
		}
	}

	return patches
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
