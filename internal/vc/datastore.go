package vc

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/vmware/govmomi/vim25/methods"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"
)

var ErrNoBrowse = errors.New(`the vCenter user needs the "Datastore > Browse datastore" privilege`)

type OrphanDisk struct {
	Datastore string
	Path      string
	Size      int64
	Capacity  int64
	Modified  time.Time
}

// skipFolders hold disks that are managed outside VM configurations:
// first-class disks, vSphere Replication targets, HA and vSAN metadata.
var skipFolders = []string{"fcd", ".sdd.sf", ".vSphere-HA", ".dvsData", ".naa.", "hbrdisk", ".snapshot", "vmkdump"}

// OrphanedDisks lists virtual disk files that no registered VM or template
// references. It only reads datastore contents.
func (c *Client) OrphanedDisks(ctx context.Context, inv *Inventory) ([]OrphanDisk, error) {
	if err := c.Ensure(ctx); err != nil {
		return nil, err
	}
	used := map[string]bool{}
	for _, vm := range inv.VMs {
		for _, f := range vm.Files {
			used[normalize(f)] = true
		}
	}
	var out []OrphanDisk
	for _, ds := range inv.Datastores {
		if !ds.Accessible || ds.Browser == "" {
			continue
		}
		files, err := c.searchDisks(ctx, ds)
		if err != nil {
			if isNoPermission(err) {
				return nil, ErrNoBrowse
			}
			return out, fmt.Errorf("datastore %s: %w", ds.Name, err)
		}
		for _, f := range files {
			if !used[normalize(f.Path)] && !skipped(f.Path) {
				out = append(out, f)
			}
		}
	}
	return out, nil
}

func (c *Client) searchDisks(ctx context.Context, ds Datastore) ([]OrphanDisk, error) {
	req := types.SearchDatastoreSubFolders_Task{
		This:          types.ManagedObjectReference{Type: "HostDatastoreBrowser", Value: ds.Browser},
		DatastorePath: "[" + ds.Name + "]",
		SearchSpec: &types.HostDatastoreBrowserSearchSpec{
			MatchPattern: []string{"*.vmdk"},
			Details:      &types.FileQueryFlags{FileType: true, FileSize: true, Modification: true},
			Query:        []types.BaseFileQuery{&types.VmDiskFileQuery{Details: &types.VmDiskFileQueryFlags{CapacityKb: true, DiskType: true}}},
		},
	}
	res, err := methods.SearchDatastoreSubFolders_Task(ctx, c.vim, &req)
	if err != nil {
		return nil, err
	}
	info, err := c.waitTask(ctx, res.Returnval)
	if err != nil {
		return nil, err
	}
	var out []OrphanDisk
	results, _ := info.Result.(types.ArrayOfHostDatastoreBrowserSearchResults)
	for _, r := range results.HostDatastoreBrowserSearchResults {
		for _, bf := range r.File {
			d, ok := bf.(*types.VmDiskFileInfo)
			if !ok {
				continue
			}
			o := OrphanDisk{Datastore: ds.Name, Path: join(r.FolderPath, d.Path), Size: d.FileSize, Capacity: d.CapacityKb * 1024}
			if d.Modification != nil {
				o.Modified = *d.Modification
			}
			out = append(out, o)
		}
	}
	return out, nil
}

// waitTask polls the task through plain property reads, so no extra vSphere
// methods have to be allowed.
func (c *Client) waitTask(ctx context.Context, ref types.ManagedObjectReference) (*types.TaskInfo, error) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		var t mo.Task
		if err := c.retrieveOne(ctx, ref, []string{"info"}, &t); err != nil {
			return nil, err
		}
		switch t.Info.State {
		case types.TaskInfoStateSuccess:
			return &t.Info, nil
		case types.TaskInfoStateError:
			if t.Info.Error != nil {
				if _, ok := t.Info.Error.Fault.(*types.NoPermission); ok {
					return nil, ErrNoBrowse
				}
				return nil, errors.New(t.Info.Error.LocalizedMessage)
			}
			return nil, errors.New("task failed")
		case types.TaskInfoStateQueued, types.TaskInfoStateRunning:
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-tick.C:
		}
	}
}

func join(folder, name string) string {
	switch {
	case strings.HasSuffix(folder, "]"):
		return folder + " " + name
	case strings.HasSuffix(folder, "/"):
		return folder + name
	}
	return folder + "/" + name
}

func normalize(p string) string {
	p = strings.TrimSpace(p)
	if i := strings.Index(p, "]"); i > 0 {
		ds, rest := p[:i+1], strings.TrimLeft(p[i+1:], " /")
		return ds + " " + path.Clean(rest)
	}
	return p
}

func skipped(p string) bool {
	for _, s := range skipFolders {
		if strings.Contains(p, "/"+s) || strings.Contains(p, "] "+s) || strings.Contains(p, s+"/") {
			return true
		}
	}
	return strings.HasPrefix(path.Base(p), "vCLS")
}

func isNoPermission(err error) bool {
	return errors.Is(err, ErrNoBrowse) || strings.Contains(err.Error(), "NoPermission") || strings.Contains(err.Error(), "Permission to perform this operation was denied")
}
