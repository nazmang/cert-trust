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
}

func NewSyncController(c client.Client, scheme *runtime.Scheme) *SyncController {
	return &SyncController{Client: c, scheme: scheme, cron: cron.New(cron.WithSeconds())}
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
	// Clear existing cron entries
	for _, e := range s.cron.Entries() {
		s.cron.Remove(e.ID)
	}

	// Stop and restart cron to ensure clean state
	s.cron.Stop()
	s.cron = cron.New(cron.WithSeconds())

	// List CertificateExport
	exportList := &unstructured.UnstructuredList{}
	exportList.SetGroupVersionKind(schemaGVKList("CertificateExport"))
	if err := s.List(ctx, exportList); err != nil {
		log.FromContext(ctx).Error(err, "failed to list CertificateExports")
		return err
	}
	log.FromContext(ctx).Info("found CertificateExports", "count", len(exportList.Items))

	// List CertificateImport
	importList := &unstructured.UnstructuredList{}
	importList.SetGroupVersionKind(schemaGVKList("CertificateImport"))
	if err := s.List(ctx, importList); err != nil {
		log.FromContext(ctx).Error(err, "failed to list CertificateImports")
		return err
	}
	log.FromContext(ctx).Info("found CertificateImports", "count", len(importList.Items))

	// Schedule exports
	for i := range exportList.Items {
		item := exportList.Items[i]
		schedule := getString(item.Object, "spec.schedule")
		if schedule == "" {
			schedule = "@every 1h"
		}
		secretRef := getString(item.Object, "spec.secretRef")
		ns := item.GetNamespace()
		name := item.GetName()
		log.FromContext(ctx).Info("scheduling export", "export", fmt.Sprintf("%s/%s", ns, name), "schedule", schedule)
		_, _ = s.cron.AddFunc(schedule, func() {
			log.FromContext(context.Background()).Info("executing export sync", "export", fmt.Sprintf("%s/%s", ns, name))
			if err := s.syncExport(context.Background(), ns, name, secretRef); err != nil {
				log.FromContext(context.Background()).Error(err, "failed to sync export", "export", fmt.Sprintf("%s/%s", ns, name))
			}
		})
	}

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
		log.FromContext(ctx).Info("scheduling import", "import", fmt.Sprintf("%s/%s", ns, name), "schedule", schedule)
		_, _ = s.cron.AddFunc(schedule, func() {
			log.FromContext(context.Background()).Info("executing import sync", "import", fmt.Sprintf("%s/%s", ns, name))
			if err := s.syncImport(context.Background(), ns, name, fromExport, targetSecret); err != nil {
				log.FromContext(context.Background()).Error(err, "failed to sync import", "import", fmt.Sprintf("%s/%s", ns, name))
			}
		})
	}

	s.cron.Start()
	log.FromContext(ctx).Info("cron scheduler started", "entries", len(s.cron.Entries()))

	// For testing: trigger immediate sync
	if len(exportList.Items) > 0 || len(importList.Items) > 0 {
		log.FromContext(ctx).Info("triggering immediate sync for testing")
		go func() {
			time.Sleep(5 * time.Second) // Wait a bit for cron to start
			for i := range exportList.Items {
				item := exportList.Items[i]
				secretRef := getString(item.Object, "spec.secretRef")
				ns := item.GetNamespace()
				name := item.GetName()
				log.FromContext(context.Background()).Info("triggering immediate export sync", "export", fmt.Sprintf("%s/%s", ns, name))
				if err := s.syncExport(context.Background(), ns, name, secretRef); err != nil {
					log.FromContext(context.Background()).Error(err, "failed to sync export", "export", fmt.Sprintf("%s/%s", ns, name))
				}
			}
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

	return nil
}

func (s *SyncController) syncExport(ctx context.Context, namespace, name, secretRef string) error {
	logger := log.FromContext(ctx).WithValues("export", fmt.Sprintf("%s/%s", namespace, name))
	var src corev1.Secret
	if err := s.Get(ctx, types.NamespacedName{Namespace: namespace, Name: secretRef}, &src); err != nil {
		logger.Error(err, "failed to get source secret")
		return err
	}
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
		logger.Error(err, "failed to get source secret")
		return err
	}
	if src.Type != corev1.SecretTypeTLS {
		return fmt.Errorf("source secret %s/%s must be type kubernetes.io/tls", src.Namespace, src.Name)
	}
	// upsert target secret
	var tgt corev1.Secret
	tgtKey := types.NamespacedName{Namespace: namespace, Name: targetSecret}
	if err := s.Get(ctx, tgtKey, &tgt); err != nil {
		tgt = corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: targetSecret},
			Type:       corev1.SecretTypeTLS,
			Data:       map[string][]byte{"tls.crt": src.Data["tls.crt"], "tls.key": src.Data["tls.key"]},
		}
		if err := s.Create(ctx, &tgt); err != nil {
			return err
		}
	} else {
		if tgt.Data == nil {
			tgt.Data = map[string][]byte{}
		}
		tgt.Type = corev1.SecretTypeTLS
		tgt.Data["tls.crt"] = src.Data["tls.crt"]
		tgt.Data["tls.key"] = src.Data["tls.key"]
		if err := s.Update(ctx, &tgt); err != nil {
			return err
		}
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
