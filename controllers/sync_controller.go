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

package controllers

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"time"

	cron "github.com/robfig/cron/v3"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	crdGroup   = "cert.trust.flolive.io"
	crdVersion = "v1"
)

type SyncController struct {
	client.Client
	scheme *runtime.Scheme
	cron   *cron.Cron
	// immediateOnStart controls whether to perform a one-time immediate sync
	// after (re)building schedules. It is guarded by immediateOnce to ensure
	// it triggers at most once per process lifetime.
	immediateOnStart bool
	immediateOnce    bool
	// Track last known resource state to avoid unnecessary rebuilds
	lastExportCount  int
	lastImportCount  int
	lastResourceHash string
}

func NewSyncController(c client.Client, scheme *runtime.Scheme, immediateOnStart bool) *SyncController {
	return &SyncController{Client: c, scheme: scheme, cron: cron.New(), immediateOnStart: immediateOnStart}
}

func (s *SyncController) Start(ctx context.Context) error {
	logger := log.FromContext(ctx)
	logger.Info("starting sync scheduler")
	go s.rescheduleLoop(ctx)
	<-ctx.Done()
	logger.Info("stopping sync scheduler")
	s.cron.Stop()
	return nil
}

func (s *SyncController) rescheduleLoop(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	for {
		if err := s.buildSchedules(ctx); err != nil {
			log.FromContext(ctx).Error(err, "failed to build schedules")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func parseNSName(defaultNS, ref string) types.NamespacedName {
	if strings.Contains(ref, "/") {
		parts := strings.SplitN(ref, "/", 2)
		return types.NamespacedName{Namespace: parts[0], Name: parts[1]}
	}
	return types.NamespacedName{Namespace: defaultNS, Name: ref}
}

func (s *SyncController) buildSchedules(ctx context.Context) error {
	// Get current resource state
	exportList := &unstructured.UnstructuredList{}
	exportList.SetGroupVersionKind(schemaGVKList("CertificateExport"))
	if err := s.List(ctx, exportList); err != nil {
		log.FromContext(ctx).Error(err, "failed to list CertificateExports")
		return err
	}
	log.FromContext(ctx).Info("found CertificateExports", "count", len(exportList.Items))

	importList := &unstructured.UnstructuredList{}
	importList.SetGroupVersionKind(schemaGVKList("CertificateImport"))
	if err := s.List(ctx, importList); err != nil {
		log.FromContext(ctx).Error(err, "failed to list CertificateImports")
		return err
	}
	log.FromContext(ctx).Info("found CertificateImports", "count", len(importList.Items))

	// Check if we need to rebuild schedules (only if resources changed)
	exportCount := len(exportList.Items)
	importCount := len(importList.Items)

	// Create a hash of all resource specs to detect content changes
	resourceHash := s.createResourceHash(exportList.Items, importList.Items)

	if exportCount == s.lastExportCount && importCount == s.lastImportCount && resourceHash == s.lastResourceHash {
		// No changes, skip rebuild
		return nil
	}

	// Update tracked state
	s.lastExportCount = exportCount
	s.lastImportCount = importCount
	s.lastResourceHash = resourceHash

	// Clear existing cron entries
	for _, e := range s.cron.Entries() {
		s.cron.Remove(e.ID)
	}

	// Stop and restart cron to ensure clean state
	s.cron.Stop()
	s.cron = cron.New()

	log.FromContext(ctx).Info("recreated cron scheduler")

	// CertificateExports don't need scheduling - they just define source secrets
	// Only CertificateImports need scheduling to copy secrets

	// Schedule imports
	for i := range importList.Items {
		item := importList.Items[i]
		schedule := getString(item.Object, "spec.schedule")
		if schedule == "" {
			schedule = "@every 1h"
		}
		fromExport := getString(item.Object, "spec.fromExport")
		targetSecret := getString(item.Object, "spec.targetSecret")
		ns := item.GetNamespace()
		name := item.GetName()

		// Validate cron expression - standard 5-field format only
		var parser cron.Parser
		if strings.HasPrefix(schedule, "@") {
			// @every, @daily, etc. - use descriptor parser
			parser = cron.NewParser(cron.Descriptor)
		} else {
			// Standard 5-field cron format: minute hour day month day-of-week
			parser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
		}

		if _, err := parser.Parse(schedule); err != nil {
			log.FromContext(ctx).Error(err, "invalid cron schedule for import", "import", fmt.Sprintf("%s/%s", ns, name), "schedule", schedule)
			continue
		}

		log.FromContext(ctx).Info("scheduling import", "import", fmt.Sprintf("%s/%s", ns, name), "schedule", schedule)
		entryID, err := s.cron.AddFunc(schedule, func() {
			logger := log.FromContext(context.Background())
			logger.Info("executing import sync", "import", fmt.Sprintf("%s/%s", ns, name))
			if err := s.syncImport(context.Background(), ns, name, fromExport, targetSecret); err != nil {
				logger.Error(err, "failed to sync import", "import", fmt.Sprintf("%s/%s", ns, name))
			} else {
				// Log next run time after successful execution
				if entry := s.cron.Entry(entryID); entry.Valid() {
					logger.Info("import sync completed", "import", fmt.Sprintf("%s/%s", ns, name), "nextRun", entry.Next)
				}
			}
		})
		if err != nil {
			log.FromContext(ctx).Error(err, "failed to schedule import", "import", fmt.Sprintf("%s/%s", ns, name), "schedule", schedule)
		} else {
			log.FromContext(ctx).Info("import scheduled successfully", "import", fmt.Sprintf("%s/%s", ns, name), "entryID", entryID)
		}
	}

	// Start cron if not already running
	if len(s.cron.Entries()) > 0 {
		s.cron.Start()
		log.FromContext(ctx).Info("cron scheduler started", "entries", len(s.cron.Entries()))

		// Debug: log next run times for all entries
		for _, entry := range s.cron.Entries() {
			log.FromContext(ctx).Info("cron entry details", "entryID", entry.ID, "nextRun", entry.Next, "valid", entry.Valid())
		}

		// Test job removed - cron is working correctly
	} else {
		log.FromContext(ctx).Info("cron scheduler has no entries to start")
	}

	// Optionally trigger a one-time immediate sync on start to prime state.
	if s.immediateOnStart && !s.immediateOnce {
		if len(importList.Items) > 0 {
			s.immediateOnce = true
			log.FromContext(ctx).Info("triggering immediate import sync on start")
			go func() {
				time.Sleep(5 * time.Second) // Wait a bit for cron to start
				for i := range importList.Items {
					item := importList.Items[i]
					fromExport := getString(item.Object, "spec.fromExport")
					targetSecret := getString(item.Object, "spec.targetSecret")
					ns := item.GetNamespace()
					name := item.GetName()
					log.FromContext(context.Background()).Info("triggering immediate import sync", "import", fmt.Sprintf("%s/%s", ns, name))
					if err := s.syncImport(context.Background(), ns, name, fromExport, targetSecret); err != nil {
						log.FromContext(context.Background()).Error(err, "failed to sync import", "import", fmt.Sprintf("%s/%s", ns, name))
					}
				}
			}()
		}
	}

	return nil
}

func (s *SyncController) syncExport(ctx context.Context, namespace, name, secretRef string) error {
	logger := log.FromContext(ctx).WithValues("export", fmt.Sprintf("%s/%s", namespace, name))

	// Verify the source secret exists and is valid
	var src corev1.Secret
	if err := s.Get(ctx, types.NamespacedName{Namespace: namespace, Name: secretRef}, &src); err != nil {
		logger.Error(err, "failed to get source secret")
		return err
	}

	if src.Type != corev1.SecretTypeTLS {
		logger.Error(fmt.Errorf("invalid secret type"), "source secret must be type kubernetes.io/tls", "type", src.Type)
		return fmt.Errorf("source secret %s/%s must be type kubernetes.io/tls", src.Namespace, src.Name)
	}

	logger.Info("export sync completed", "secretRef", secretRef, "secretType", src.Type)

	// Update status.lastSyncTime on the export (best-effort)
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(schemaGVK("CertificateExport"))
	if err := s.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, obj); err == nil {
		setString(obj.Object, "status.lastSyncTime", time.Now().UTC().Format(time.RFC3339))
		_ = s.Status().Update(ctx, obj)
	}

	return nil
}

func (s *SyncController) syncImport(ctx context.Context, namespace, name, fromExport, targetSecret string) error {
	logger := log.FromContext(ctx).WithValues("import", fmt.Sprintf("%s/%s", namespace, name))
	// resolve export
	expKey := parseNSName(namespace, fromExport)
	exp := &unstructured.Unstructured{}
	exp.SetGroupVersionKind(schemaGVK("CertificateExport"))
	if err := s.Get(ctx, expKey, exp); err != nil {
		logger.Error(err, "failed to get export")
		return err
	}
	secretRef := getString(exp.Object, "spec.secretRef")
	// read source secret
	var src corev1.Secret
	if err := s.Get(ctx, types.NamespacedName{Namespace: exp.GetNamespace(), Name: secretRef}, &src); err != nil {
		logger.Error(err, "failed to get source secret", "secretRef", secretRef, "namespace", exp.GetNamespace())
		return err
	}
	if src.Type != corev1.SecretTypeTLS {
		return fmt.Errorf("source secret %s/%s must be type kubernetes.io/tls", src.Namespace, src.Name)
	}

	// Debug: log source secret info
	logger.Info("source secret found", "secretRef", secretRef, "type", src.Type, "hasTlsCrt", src.Data["tls.crt"] != nil, "hasTlsKey", src.Data["tls.key"] != nil, "hasCaCrt", src.Data["ca.crt"] != nil)
	// upsert target secret
	var tgt corev1.Secret
	tgtKey := types.NamespacedName{Namespace: namespace, Name: targetSecret}
	if err := s.Get(ctx, tgtKey, &tgt); err != nil {
		// Secret doesn't exist, create it
		tgtData := map[string][]byte{
			"tls.crt": src.Data["tls.crt"],
			"tls.key": src.Data["tls.key"],
		}
		// Copy ca.crt if it exists in the source secret
		if src.Data["ca.crt"] != nil {
			tgtData["ca.crt"] = src.Data["ca.crt"]
		}
		tgt = corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: targetSecret},
			Type:       corev1.SecretTypeTLS,
			Data:       tgtData,
		}
		if err := s.Create(ctx, &tgt); err != nil {
			logger.Error(err, "failed to create target secret", "targetSecret", targetSecret, "namespace", namespace)
			return err
		}
		logger.Info("created target secret", "targetSecret", targetSecret, "namespace", namespace)
	} else {
		// Secret exists, update it
		if tgt.Data == nil {
			tgt.Data = map[string][]byte{}
		}
		tgt.Type = corev1.SecretTypeTLS
		tgt.Data["tls.crt"] = src.Data["tls.crt"]
		tgt.Data["tls.key"] = src.Data["tls.key"]
		// Copy ca.crt if it exists in the source secret
		if src.Data["ca.crt"] != nil {
			tgt.Data["ca.crt"] = src.Data["ca.crt"]
		} else {
			// Remove ca.crt if it doesn't exist in source
			delete(tgt.Data, "ca.crt")
		}
		if err := s.Update(ctx, &tgt); err != nil {
			logger.Error(err, "failed to update target secret", "targetSecret", targetSecret, "namespace", namespace)
			return err
		}
		logger.Info("updated target secret", "targetSecret", targetSecret, "namespace", namespace)
	}
	// Update status.lastSyncTime on the import (best-effort)
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(schemaGVK("CertificateImport"))
	if err := s.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, obj); err == nil {
		setString(obj.Object, "status.lastSyncTime", time.Now().UTC().Format(time.RFC3339))
		_ = s.Status().Update(ctx, obj)
	}
	return nil
}

// helpers
func schemaGVK(kind string) schema.GroupVersionKind {
	return schema.GroupVersion{Group: crdGroup, Version: crdVersion}.WithKind(kind)
}

func schemaGVKList(kind string) schema.GroupVersionKind {
	return schema.GroupVersion{Group: crdGroup, Version: crdVersion}.WithKind(kind + "List")
}

func getString(obj map[string]interface{}, path string) string {
	parts := strings.Split(path, ".")
	var cur interface{} = obj
	for _, p := range parts {
		m, ok := cur.(map[string]interface{})
		if !ok {
			return ""
		}
		cur = m[p]
	}
	if s, ok := cur.(string); ok {
		return s
	}
	return ""
}

func setString(obj map[string]interface{}, path, value string) {
	parts := strings.Split(path, ".")
	cur := obj
	for i, p := range parts {
		if i == len(parts)-1 {
			cur[p] = value
			return
		}
		nxt, ok := cur[p].(map[string]interface{})
		if !ok {
			nxt = map[string]interface{}{}
			cur[p] = nxt
		}
		cur = nxt
	}
}

func (s *SyncController) createResourceHash(exports, imports []unstructured.Unstructured) string {
	var hashInput strings.Builder

	// Add export specs to hash
	for _, item := range exports {
		hashInput.WriteString(fmt.Sprintf("export:%s/%s:", item.GetNamespace(), item.GetName()))
		hashInput.WriteString(fmt.Sprintf("secretRef:%s:", getString(item.Object, "spec.secretRef")))
	}

	// Add import specs to hash
	for _, item := range imports {
		hashInput.WriteString(fmt.Sprintf("import:%s/%s:", item.GetNamespace(), item.GetName()))
		hashInput.WriteString(fmt.Sprintf("fromExport:%s:", getString(item.Object, "spec.fromExport")))
		hashInput.WriteString(fmt.Sprintf("targetSecret:%s:", getString(item.Object, "spec.targetSecret")))
		hashInput.WriteString(fmt.Sprintf("schedule:%s:", getString(item.Object, "spec.schedule")))
	}

	hash := sha256.Sum256([]byte(hashInput.String()))
	return fmt.Sprintf("%x", hash)
}
