//go:build !ignore_autogenerated
// +build !ignore_autogenerated

/*

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

// Code generated by controller-gen. DO NOT EDIT.

package v1beta1

import (
	runtime "k8s.io/apimachinery/pkg/runtime"
)

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *Branch) DeepCopyInto(out *Branch) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new Branch.
func (in *Branch) DeepCopy() *Branch {
	if in == nil {
		return nil
	}
	out := new(Branch)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *Build) DeepCopyInto(out *Build) {
	*out = *in
	if in.Priority != nil {
		in, out := &in.Priority, &out.Priority
		*out = new(int)
		**out = **in
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new Build.
func (in *Build) DeepCopy() *Build {
	if in == nil {
		return nil
	}
	out := new(Build)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *LagoonAdvancedTaskInfo) DeepCopyInto(out *LagoonAdvancedTaskInfo) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new LagoonAdvancedTaskInfo.
func (in *LagoonAdvancedTaskInfo) DeepCopy() *LagoonAdvancedTaskInfo {
	if in == nil {
		return nil
	}
	out := new(LagoonAdvancedTaskInfo)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *LagoonBuild) DeepCopyInto(out *LagoonBuild) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
	if in.StatusMessages != nil {
		in, out := &in.StatusMessages, &out.StatusMessages
		*out = new(LagoonStatusMessages)
		(*in).DeepCopyInto(*out)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new LagoonBuild.
func (in *LagoonBuild) DeepCopy() *LagoonBuild {
	if in == nil {
		return nil
	}
	out := new(LagoonBuild)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *LagoonBuild) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *LagoonBuildConditions) DeepCopyInto(out *LagoonBuildConditions) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new LagoonBuildConditions.
func (in *LagoonBuildConditions) DeepCopy() *LagoonBuildConditions {
	if in == nil {
		return nil
	}
	out := new(LagoonBuildConditions)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *LagoonBuildList) DeepCopyInto(out *LagoonBuildList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]LagoonBuild, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new LagoonBuildList.
func (in *LagoonBuildList) DeepCopy() *LagoonBuildList {
	if in == nil {
		return nil
	}
	out := new(LagoonBuildList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *LagoonBuildList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *LagoonBuildSpec) DeepCopyInto(out *LagoonBuildSpec) {
	*out = *in
	in.Build.DeepCopyInto(&out.Build)
	in.Project.DeepCopyInto(&out.Project)
	out.Branch = in.Branch
	out.Pullrequest = in.Pullrequest
	out.Promote = in.Promote
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new LagoonBuildSpec.
func (in *LagoonBuildSpec) DeepCopy() *LagoonBuildSpec {
	if in == nil {
		return nil
	}
	out := new(LagoonBuildSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *LagoonBuildStatus) DeepCopyInto(out *LagoonBuildStatus) {
	*out = *in
	if in.Conditions != nil {
		in, out := &in.Conditions, &out.Conditions
		*out = make([]LagoonBuildConditions, len(*in))
		copy(*out, *in)
	}
	if in.Log != nil {
		in, out := &in.Log, &out.Log
		*out = make([]byte, len(*in))
		copy(*out, *in)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new LagoonBuildStatus.
func (in *LagoonBuildStatus) DeepCopy() *LagoonBuildStatus {
	if in == nil {
		return nil
	}
	out := new(LagoonBuildStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *LagoonLog) DeepCopyInto(out *LagoonLog) {
	*out = *in
	if in.Meta != nil {
		in, out := &in.Meta, &out.Meta
		*out = new(LagoonLogMeta)
		(*in).DeepCopyInto(*out)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new LagoonLog.
func (in *LagoonLog) DeepCopy() *LagoonLog {
	if in == nil {
		return nil
	}
	out := new(LagoonLog)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *LagoonLogMeta) DeepCopyInto(out *LagoonLogMeta) {
	*out = *in
	if in.EnvironmentID != nil {
		in, out := &in.EnvironmentID, &out.EnvironmentID
		*out = new(uint)
		**out = **in
	}
	if in.ProjectID != nil {
		in, out := &in.ProjectID, &out.ProjectID
		*out = new(uint)
		**out = **in
	}
	if in.Routes != nil {
		in, out := &in.Routes, &out.Routes
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
	if in.Services != nil {
		in, out := &in.Services, &out.Services
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
	if in.EnvironmentServices != nil {
		in, out := &in.EnvironmentServices, &out.EnvironmentServices
		*out = make([]LagoonService, len(*in))
		copy(*out, *in)
	}
	if in.Task != nil {
		in, out := &in.Task, &out.Task
		*out = new(LagoonTaskInfo)
		**out = **in
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new LagoonLogMeta.
func (in *LagoonLogMeta) DeepCopy() *LagoonLogMeta {
	if in == nil {
		return nil
	}
	out := new(LagoonLogMeta)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *LagoonMessage) DeepCopyInto(out *LagoonMessage) {
	*out = *in
	if in.Meta != nil {
		in, out := &in.Meta, &out.Meta
		*out = new(LagoonLogMeta)
		(*in).DeepCopyInto(*out)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new LagoonMessage.
func (in *LagoonMessage) DeepCopy() *LagoonMessage {
	if in == nil {
		return nil
	}
	out := new(LagoonMessage)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *LagoonMiscBackupInfo) DeepCopyInto(out *LagoonMiscBackupInfo) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new LagoonMiscBackupInfo.
func (in *LagoonMiscBackupInfo) DeepCopy() *LagoonMiscBackupInfo {
	if in == nil {
		return nil
	}
	out := new(LagoonMiscBackupInfo)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *LagoonMiscInfo) DeepCopyInto(out *LagoonMiscInfo) {
	*out = *in
	if in.Backup != nil {
		in, out := &in.Backup, &out.Backup
		*out = new(LagoonMiscBackupInfo)
		**out = **in
	}
	if in.MiscResource != nil {
		in, out := &in.MiscResource, &out.MiscResource
		*out = make([]byte, len(*in))
		copy(*out, *in)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new LagoonMiscInfo.
func (in *LagoonMiscInfo) DeepCopy() *LagoonMiscInfo {
	if in == nil {
		return nil
	}
	out := new(LagoonMiscInfo)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *LagoonService) DeepCopyInto(out *LagoonService) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new LagoonService.
func (in *LagoonService) DeepCopy() *LagoonService {
	if in == nil {
		return nil
	}
	out := new(LagoonService)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *LagoonStatusMessages) DeepCopyInto(out *LagoonStatusMessages) {
	*out = *in
	if in.StatusMessage != nil {
		in, out := &in.StatusMessage, &out.StatusMessage
		*out = new(LagoonLog)
		(*in).DeepCopyInto(*out)
	}
	if in.BuildLogMessage != nil {
		in, out := &in.BuildLogMessage, &out.BuildLogMessage
		*out = new(LagoonLog)
		(*in).DeepCopyInto(*out)
	}
	if in.TaskLogMessage != nil {
		in, out := &in.TaskLogMessage, &out.TaskLogMessage
		*out = new(LagoonLog)
		(*in).DeepCopyInto(*out)
	}
	if in.EnvironmentMessage != nil {
		in, out := &in.EnvironmentMessage, &out.EnvironmentMessage
		*out = new(LagoonMessage)
		(*in).DeepCopyInto(*out)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new LagoonStatusMessages.
func (in *LagoonStatusMessages) DeepCopy() *LagoonStatusMessages {
	if in == nil {
		return nil
	}
	out := new(LagoonStatusMessages)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *LagoonTask) DeepCopyInto(out *LagoonTask) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
	if in.StatusMessages != nil {
		in, out := &in.StatusMessages, &out.StatusMessages
		*out = new(LagoonStatusMessages)
		(*in).DeepCopyInto(*out)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new LagoonTask.
func (in *LagoonTask) DeepCopy() *LagoonTask {
	if in == nil {
		return nil
	}
	out := new(LagoonTask)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *LagoonTask) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *LagoonTaskConditions) DeepCopyInto(out *LagoonTaskConditions) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new LagoonTaskConditions.
func (in *LagoonTaskConditions) DeepCopy() *LagoonTaskConditions {
	if in == nil {
		return nil
	}
	out := new(LagoonTaskConditions)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *LagoonTaskEnvironment) DeepCopyInto(out *LagoonTaskEnvironment) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new LagoonTaskEnvironment.
func (in *LagoonTaskEnvironment) DeepCopy() *LagoonTaskEnvironment {
	if in == nil {
		return nil
	}
	out := new(LagoonTaskEnvironment)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *LagoonTaskInfo) DeepCopyInto(out *LagoonTaskInfo) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new LagoonTaskInfo.
func (in *LagoonTaskInfo) DeepCopy() *LagoonTaskInfo {
	if in == nil {
		return nil
	}
	out := new(LagoonTaskInfo)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *LagoonTaskList) DeepCopyInto(out *LagoonTaskList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]LagoonTask, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new LagoonTaskList.
func (in *LagoonTaskList) DeepCopy() *LagoonTaskList {
	if in == nil {
		return nil
	}
	out := new(LagoonTaskList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *LagoonTaskList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *LagoonTaskProject) DeepCopyInto(out *LagoonTaskProject) {
	*out = *in
	in.Variables.DeepCopyInto(&out.Variables)
	if in.Organization != nil {
		in, out := &in.Organization, &out.Organization
		*out = new(Organization)
		(*in).DeepCopyInto(*out)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new LagoonTaskProject.
func (in *LagoonTaskProject) DeepCopy() *LagoonTaskProject {
	if in == nil {
		return nil
	}
	out := new(LagoonTaskProject)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *LagoonTaskSpec) DeepCopyInto(out *LagoonTaskSpec) {
	*out = *in
	out.Task = in.Task
	in.Project.DeepCopyInto(&out.Project)
	out.Environment = in.Environment
	if in.Misc != nil {
		in, out := &in.Misc, &out.Misc
		*out = new(LagoonMiscInfo)
		(*in).DeepCopyInto(*out)
	}
	if in.AdvancedTask != nil {
		in, out := &in.AdvancedTask, &out.AdvancedTask
		*out = new(LagoonAdvancedTaskInfo)
		**out = **in
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new LagoonTaskSpec.
func (in *LagoonTaskSpec) DeepCopy() *LagoonTaskSpec {
	if in == nil {
		return nil
	}
	out := new(LagoonTaskSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *LagoonTaskStatus) DeepCopyInto(out *LagoonTaskStatus) {
	*out = *in
	if in.Conditions != nil {
		in, out := &in.Conditions, &out.Conditions
		*out = make([]LagoonTaskConditions, len(*in))
		copy(*out, *in)
	}
	if in.Log != nil {
		in, out := &in.Log, &out.Log
		*out = make([]byte, len(*in))
		copy(*out, *in)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new LagoonTaskStatus.
func (in *LagoonTaskStatus) DeepCopy() *LagoonTaskStatus {
	if in == nil {
		return nil
	}
	out := new(LagoonTaskStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *LagoonVariables) DeepCopyInto(out *LagoonVariables) {
	*out = *in
	if in.Project != nil {
		in, out := &in.Project, &out.Project
		*out = make([]byte, len(*in))
		copy(*out, *in)
	}
	if in.Environment != nil {
		in, out := &in.Environment, &out.Environment
		*out = make([]byte, len(*in))
		copy(*out, *in)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new LagoonVariables.
func (in *LagoonVariables) DeepCopy() *LagoonVariables {
	if in == nil {
		return nil
	}
	out := new(LagoonVariables)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *Monitoring) DeepCopyInto(out *Monitoring) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new Monitoring.
func (in *Monitoring) DeepCopy() *Monitoring {
	if in == nil {
		return nil
	}
	out := new(Monitoring)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *Organization) DeepCopyInto(out *Organization) {
	*out = *in
	if in.ID != nil {
		in, out := &in.ID, &out.ID
		*out = new(uint)
		**out = **in
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new Organization.
func (in *Organization) DeepCopy() *Organization {
	if in == nil {
		return nil
	}
	out := new(Organization)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *Project) DeepCopyInto(out *Project) {
	*out = *in
	if in.ID != nil {
		in, out := &in.ID, &out.ID
		*out = new(uint)
		**out = **in
	}
	if in.EnvironmentID != nil {
		in, out := &in.EnvironmentID, &out.EnvironmentID
		*out = new(uint)
		**out = **in
	}
	if in.Key != nil {
		in, out := &in.Key, &out.Key
		*out = make([]byte, len(*in))
		copy(*out, *in)
	}
	out.Monitoring = in.Monitoring
	in.Variables.DeepCopyInto(&out.Variables)
	if in.EnvironmentIdling != nil {
		in, out := &in.EnvironmentIdling, &out.EnvironmentIdling
		*out = new(int)
		**out = **in
	}
	if in.ProjectIdling != nil {
		in, out := &in.ProjectIdling, &out.ProjectIdling
		*out = new(int)
		**out = **in
	}
	if in.StorageCalculator != nil {
		in, out := &in.StorageCalculator, &out.StorageCalculator
		*out = new(int)
		**out = **in
	}
	if in.Organization != nil {
		in, out := &in.Organization, &out.Organization
		*out = new(Organization)
		(*in).DeepCopyInto(*out)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new Project.
func (in *Project) DeepCopy() *Project {
	if in == nil {
		return nil
	}
	out := new(Project)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *Promote) DeepCopyInto(out *Promote) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new Promote.
func (in *Promote) DeepCopy() *Promote {
	if in == nil {
		return nil
	}
	out := new(Promote)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *Pullrequest) DeepCopyInto(out *Pullrequest) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new Pullrequest.
func (in *Pullrequest) DeepCopy() *Pullrequest {
	if in == nil {
		return nil
	}
	out := new(Pullrequest)
	in.DeepCopyInto(out)
	return out
}
