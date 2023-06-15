package deploy

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/kustomize/api/filesys"
	"sigs.k8s.io/kustomize/api/krusty"
	"sigs.k8s.io/kustomize/api/resmap"

	"github.com/opendatahub-io/opendatahub-operator/pkg/plugins"
)

// downloadManifests function performs following tasks:
// 1. Given remote URI, download manifests, else extract local bundle
// 2. It saves the manifests in the odh-manifests/component-name/ folder
func downloadManifests(uri string) error {
	// Get the component repo from the given url
	// e.g  https://github.com/example/tarball/master\
	var reader io.Reader
	if uri != "" {
		resp, err := http.Get(uri)
		if err != nil {
			return fmt.Errorf("error downloading manifests: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("error downloading manifests: %v HTTP status", resp.StatusCode)
		}
		reader = resp.Body
	} else {
		file, err := os.Open("/opt/manifests/odh-manifests.tar.gz")
		if err != nil {
			return err
		}
		defer file.Close()
		reader = file
	}

	// Create a new gzip reader
	gzipReader, err := gzip.NewReader(reader)
	if err != nil {
		return fmt.Errorf("error creating gzip reader: %v", err)
	}
	defer gzipReader.Close()

	// Create a new TAR reader
	tarReader := tar.NewReader(gzipReader)
	// Empty dir
	header, err := tarReader.Next()
	if err == io.EOF {
		return err
	}
	// Create manifest directory

	for {
		header, err = tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		// Determine the file or directory path to extract to
		target := filepath.Join("/opt/manifests/odh-manifests", header.Name)

		if header.Typeflag == tar.TypeDir {
			// Create directories
			err = os.MkdirAll(target, os.ModePerm)
			if err != nil {
				return err
			}
		} else if header.Typeflag == tar.TypeReg {
			// Extract regular files
			outputFile, err := os.Create(target)
			if err != nil {
				return err
			}
			defer outputFile.Close()

			_, err = io.Copy(outputFile, tarReader)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func deployManifestsFromPath(cli client.Client, manifestPath, namespace string) error {

	// Render the Kustomize manifests
	k := krusty.MakeKustomizer(krusty.MakeDefaultOptions())
	fs := filesys.MakeFsOnDisk()

	// Create resmap
	// Use kustomization file under manifestPath or use `default` overlay
	var resMap resmap.ResMap
	_, err := os.Stat(manifestPath + "/kustomization.yaml")
	if err != nil {
		if os.IsNotExist(err) {
			resMap, err = k.Run(fs, manifestPath+"/default")
		}
	} else {
		resMap, err = k.Run(fs, manifestPath)
	}

	if err != nil {
		return fmt.Errorf("error during resmap resources: %v", err)
	}

	// Apply NamespaceTransformer Plugin
	if err := plugins.ApplyNamespacePlugin(namespace, resMap); err != nil {
		return err
	}

	objs, err := getResources(resMap)
	if err != nil {
		return err
	}

	// Create or update resources in the cluster
	for _, obj := range objs {

		err = createOrUpdate(context.TODO(), cli, obj)
		if err != nil {
			return err
		}
	}

	return nil

}

func getResources(resMap resmap.ResMap) ([]*unstructured.Unstructured, error) {
	var resources []*unstructured.Unstructured
	for _, res := range resMap.Resources() {
		u := &unstructured.Unstructured{}
		err := yaml.Unmarshal([]byte(res.MustYaml()), u)
		if err != nil {
			return nil, err
		}
		resources = append(resources, u)
	}

	return resources, nil
}

func createOrUpdate(ctx context.Context, cli client.Client, obj *unstructured.Unstructured) error {
	fmt.Printf("Creating resource :%v", obj.UnstructuredContent())
	found := obj.DeepCopy()
	err := cli.Get(ctx, types.NamespacedName{Name: obj.GetName(), Namespace: obj.GetNamespace()}, found)
	if err != nil && errors.IsNotFound(err) {
		//// Set the owner reference for garbage collection
		//if err := controllerutil.SetControllerReference(<dscInitialization>, obj, r.Scheme); err != nil {
		//	return err
		//}
		// Create the resource if it doesn't exist
		return cli.Create(ctx, obj)
	} else if err != nil {
		return err
	}

	// Update the resource if it exists
	obj.SetResourceVersion(found.GetResourceVersion())
	return cli.Update(ctx, obj)
}
