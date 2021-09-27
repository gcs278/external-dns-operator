// +build !ignore_autogenerated

/*
Copyright 2021.

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

package v1alpha1

import (
	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"
)

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ExternalDNS) DeepCopyInto(out *ExternalDNS) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ExternalDNS.
func (in *ExternalDNS) DeepCopy() *ExternalDNS {
	if in == nil {
		return nil
	}
	out := new(ExternalDNS)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *ExternalDNS) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ExternalDNSAWSProviderOptions) DeepCopyInto(out *ExternalDNSAWSProviderOptions) {
	*out = *in
	out.Credentials = in.Credentials
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ExternalDNSAWSProviderOptions.
func (in *ExternalDNSAWSProviderOptions) DeepCopy() *ExternalDNSAWSProviderOptions {
	if in == nil {
		return nil
	}
	out := new(ExternalDNSAWSProviderOptions)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ExternalDNSAzureProviderOptions) DeepCopyInto(out *ExternalDNSAzureProviderOptions) {
	*out = *in
	out.ConfigFile = in.ConfigFile
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ExternalDNSAzureProviderOptions.
func (in *ExternalDNSAzureProviderOptions) DeepCopy() *ExternalDNSAzureProviderOptions {
	if in == nil {
		return nil
	}
	out := new(ExternalDNSAzureProviderOptions)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ExternalDNSBlueCatProviderOptions) DeepCopyInto(out *ExternalDNSBlueCatProviderOptions) {
	*out = *in
	out.ConfigFile = in.ConfigFile
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ExternalDNSBlueCatProviderOptions.
func (in *ExternalDNSBlueCatProviderOptions) DeepCopy() *ExternalDNSBlueCatProviderOptions {
	if in == nil {
		return nil
	}
	out := new(ExternalDNSBlueCatProviderOptions)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ExternalDNSCRDSourceOptions) DeepCopyInto(out *ExternalDNSCRDSourceOptions) {
	*out = *in
	if in.LabelFilter != nil {
		in, out := &in.LabelFilter, &out.LabelFilter
		*out = new(metav1.LabelSelector)
		(*in).DeepCopyInto(*out)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ExternalDNSCRDSourceOptions.
func (in *ExternalDNSCRDSourceOptions) DeepCopy() *ExternalDNSCRDSourceOptions {
	if in == nil {
		return nil
	}
	out := new(ExternalDNSCRDSourceOptions)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ExternalDNSDomain) DeepCopyInto(out *ExternalDNSDomain) {
	*out = *in
	in.ExternalDNSDomainUnion.DeepCopyInto(&out.ExternalDNSDomainUnion)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ExternalDNSDomain.
func (in *ExternalDNSDomain) DeepCopy() *ExternalDNSDomain {
	if in == nil {
		return nil
	}
	out := new(ExternalDNSDomain)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ExternalDNSDomainUnion) DeepCopyInto(out *ExternalDNSDomainUnion) {
	*out = *in
	if in.Name != nil {
		in, out := &in.Name, &out.Name
		*out = new(string)
		**out = **in
	}
	if in.Pattern != nil {
		in, out := &in.Pattern, &out.Pattern
		*out = new(string)
		**out = **in
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ExternalDNSDomainUnion.
func (in *ExternalDNSDomainUnion) DeepCopy() *ExternalDNSDomainUnion {
	if in == nil {
		return nil
	}
	out := new(ExternalDNSDomainUnion)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ExternalDNSGCPProviderOptions) DeepCopyInto(out *ExternalDNSGCPProviderOptions) {
	*out = *in
	if in.Project != nil {
		in, out := &in.Project, &out.Project
		*out = new(string)
		**out = **in
	}
	out.Credentials = in.Credentials
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ExternalDNSGCPProviderOptions.
func (in *ExternalDNSGCPProviderOptions) DeepCopy() *ExternalDNSGCPProviderOptions {
	if in == nil {
		return nil
	}
	out := new(ExternalDNSGCPProviderOptions)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ExternalDNSInfobloxProviderOptions) DeepCopyInto(out *ExternalDNSInfobloxProviderOptions) {
	*out = *in
	out.Credentials = in.Credentials
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ExternalDNSInfobloxProviderOptions.
func (in *ExternalDNSInfobloxProviderOptions) DeepCopy() *ExternalDNSInfobloxProviderOptions {
	if in == nil {
		return nil
	}
	out := new(ExternalDNSInfobloxProviderOptions)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ExternalDNSList) DeepCopyInto(out *ExternalDNSList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]ExternalDNS, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ExternalDNSList.
func (in *ExternalDNSList) DeepCopy() *ExternalDNSList {
	if in == nil {
		return nil
	}
	out := new(ExternalDNSList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *ExternalDNSList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ExternalDNSProvider) DeepCopyInto(out *ExternalDNSProvider) {
	*out = *in
	if in.AWS != nil {
		in, out := &in.AWS, &out.AWS
		*out = new(ExternalDNSAWSProviderOptions)
		**out = **in
	}
	if in.GCP != nil {
		in, out := &in.GCP, &out.GCP
		*out = new(ExternalDNSGCPProviderOptions)
		(*in).DeepCopyInto(*out)
	}
	if in.Azure != nil {
		in, out := &in.Azure, &out.Azure
		*out = new(ExternalDNSAzureProviderOptions)
		**out = **in
	}
	if in.BlueCat != nil {
		in, out := &in.BlueCat, &out.BlueCat
		*out = new(ExternalDNSBlueCatProviderOptions)
		**out = **in
	}
	if in.Infoblox != nil {
		in, out := &in.Infoblox, &out.Infoblox
		*out = new(ExternalDNSInfobloxProviderOptions)
		**out = **in
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ExternalDNSProvider.
func (in *ExternalDNSProvider) DeepCopy() *ExternalDNSProvider {
	if in == nil {
		return nil
	}
	out := new(ExternalDNSProvider)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ExternalDNSServiceSourceOptions) DeepCopyInto(out *ExternalDNSServiceSourceOptions) {
	*out = *in
	if in.ServiceType != nil {
		in, out := &in.ServiceType, &out.ServiceType
		*out = make([]v1.ServiceType, len(*in))
		copy(*out, *in)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ExternalDNSServiceSourceOptions.
func (in *ExternalDNSServiceSourceOptions) DeepCopy() *ExternalDNSServiceSourceOptions {
	if in == nil {
		return nil
	}
	out := new(ExternalDNSServiceSourceOptions)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ExternalDNSSource) DeepCopyInto(out *ExternalDNSSource) {
	*out = *in
	in.ExternalDNSSourceUnion.DeepCopyInto(&out.ExternalDNSSourceUnion)
	if in.FQDNTemplate != nil {
		in, out := &in.FQDNTemplate, &out.FQDNTemplate
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ExternalDNSSource.
func (in *ExternalDNSSource) DeepCopy() *ExternalDNSSource {
	if in == nil {
		return nil
	}
	out := new(ExternalDNSSource)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ExternalDNSSourceUnion) DeepCopyInto(out *ExternalDNSSourceUnion) {
	*out = *in
	if in.AnnotationFilter != nil {
		in, out := &in.AnnotationFilter, &out.AnnotationFilter
		*out = make(map[string]string, len(*in))
		for key, val := range *in {
			(*out)[key] = val
		}
	}
	if in.Namespace != nil {
		in, out := &in.Namespace, &out.Namespace
		*out = new(string)
		**out = **in
	}
	if in.Service != nil {
		in, out := &in.Service, &out.Service
		*out = new(ExternalDNSServiceSourceOptions)
		(*in).DeepCopyInto(*out)
	}
	if in.CRD != nil {
		in, out := &in.CRD, &out.CRD
		*out = new(ExternalDNSCRDSourceOptions)
		(*in).DeepCopyInto(*out)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ExternalDNSSourceUnion.
func (in *ExternalDNSSourceUnion) DeepCopy() *ExternalDNSSourceUnion {
	if in == nil {
		return nil
	}
	out := new(ExternalDNSSourceUnion)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ExternalDNSSpec) DeepCopyInto(out *ExternalDNSSpec) {
	*out = *in
	if in.Domains != nil {
		in, out := &in.Domains, &out.Domains
		*out = make([]ExternalDNSDomain, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	in.Provider.DeepCopyInto(&out.Provider)
	in.Source.DeepCopyInto(&out.Source)
	if in.Zones != nil {
		in, out := &in.Zones, &out.Zones
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ExternalDNSSpec.
func (in *ExternalDNSSpec) DeepCopy() *ExternalDNSSpec {
	if in == nil {
		return nil
	}
	out := new(ExternalDNSSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ExternalDNSStatus) DeepCopyInto(out *ExternalDNSStatus) {
	*out = *in
	if in.Conditions != nil {
		in, out := &in.Conditions, &out.Conditions
		*out = make([]metav1.Condition, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	if in.Zones != nil {
		in, out := &in.Zones, &out.Zones
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ExternalDNSStatus.
func (in *ExternalDNSStatus) DeepCopy() *ExternalDNSStatus {
	if in == nil {
		return nil
	}
	out := new(ExternalDNSStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *SecretReference) DeepCopyInto(out *SecretReference) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new SecretReference.
func (in *SecretReference) DeepCopy() *SecretReference {
	if in == nil {
		return nil
	}
	out := new(SecretReference)
	in.DeepCopyInto(out)
	return out
}
