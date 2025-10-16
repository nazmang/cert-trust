// Copyright 2025 cert-trust contributors
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

package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=cex
// +kubebuilder:printcolumn:name=Secret,JSONPath=.spec.secretRef,description=Source TLS secret,type=string
// +kubebuilder:printcolumn:name=Schedule,JSONPath=.spec.schedule,description=Cron schedule,type=string
// CertificateExport specifies a source secret to export from this namespace
// to other namespaces.
type CertificateExport struct {
	metav1.TypeMeta   `json:\",inline\"`
	metav1.ObjectMeta `json:\"metadata,omitempty\"`

	Spec   CertificateExportSpec   `json:\"spec,omitempty\"`
	Status CertificateExportStatus `json:\"status,omitempty\"`
}

type CertificateExportSpec struct {
	// SecretRef is the name of a TLS secret in the same namespace
	SecretRef string `json:\"secretRef\"`
	// Schedule is a cron expression determining when to refresh data from the source
	Schedule string `json:\"schedule,omitempty\"`
}

type CertificateExportStatus struct {
	// LastSyncTime records the most recent successful sync time
	LastSyncTime *metav1.Time `json:\"lastSyncTime,omitempty\"`
}

// +kubebuilder:object:root=true
type CertificateExportList struct {
	metav1.TypeMeta `json:\",inline\"`
	metav1.ListMeta `json:\"metadata,omitempty\"`
	Items           []CertificateExport `json:\"items\"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=cimp
// +kubebuilder:printcolumn:name=From,JSONPath=.spec.fromExport,description=Source export,type=string
// +kubebuilder:printcolumn:name=Target,JSONPath=.spec.targetSecret,description=Target secret,type=string
// +kubebuilder:printcolumn:name=Schedule,JSONPath=.spec.schedule,description=Cron schedule,type=string
// CertificateImport references a CertificateExport and manages a target secret
// in this namespace.
type CertificateImport struct {
	metav1.TypeMeta   `json:\",inline\"`
	metav1.ObjectMeta `json:\"metadata,omitempty\"`

	Spec   CertificateImportSpec   `json:\"spec,omitempty\"`
	Status CertificateImportStatus `json:\"status,omitempty\"`
}

type CertificateImportSpec struct {
	// FromExport is in the format namespace/name or just name (same namespace)
	FromExport string `json:\"fromExport\"`
	// TargetSecret is the name of the secret to create/update in this namespace
	TargetSecret string `json:\"targetSecret\"`
	// Schedule is a cron expression determining when to refresh data from the source
	Schedule string `json:\"schedule,omitempty\"`
}

type CertificateImportStatus struct {
	// LastSyncTime records the most recent successful sync time
	LastSyncTime *metav1.Time `json:\"lastSyncTime,omitempty\"`
}

// +kubebuilder:object:root=true
type CertificateImportList struct {
	metav1.TypeMeta `json:\",inline\"`
	metav1.ListMeta `json:\"metadata,omitempty\"`
	Items           []CertificateImport `json:\"items\"`
}
