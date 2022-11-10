# Copyright 2016--2022 Lightbits Labs Ltd.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# you may obtain a copy of the License at
#
#      http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

%{!?dist: %define dist 1}

Name:     discovery-client
Version:  %{?version}%{!?version:9.9.9}
Release:  %{dist}
Group:    System
Summary:  NVMe/TCP Discovery Client
Vendor:   Lightbits Labs LTD
License:  Lightbits License
URL: 	  https://github.com/LightBitsLabs/discovery-client
Packager: support@lightbitslabs.com
Source0:  https://github.com/LightBitsLabs/discovery-client/archive/v%{version}.tar.gz

BuildArch:      x86_64

%description
Discovery client for NVMe/TCP initiators.

%prep

%build

%install
rm -rf %{buildroot}/*
rm -rf %{_topdir}/RPMS/*

install -dp %{buildroot}%{_bindir}
install -dp %{buildroot}/etc/systemd/system

## client related files
install -p -m 755 %{_builddir}/build/discovery-client %{buildroot}%{_bindir}/discovery-client
install -dp -m 755 %{buildroot}/etc/discovery-client
cp -ar %{_builddir}/etc/discovery-client/discovery-client.yaml %{buildroot}/etc/discovery-client/discovery-client.yaml
install -p -m 644 %{_builddir}/etc/systemd/system/discovery-client.service %{buildroot}/etc/systemd/system/discovery-client.service

install -dp %{buildroot}/var/lib/discovery-client/docs
cp -a %{_builddir}/README.md      %{buildroot}/var/lib/discovery-client/docs/

%files
%defattr(-,root,root,-)
/var/lib/discovery-client/docs
%{_bindir}/discovery-client
/etc/systemd/system/discovery-client.service
%config(noreplace) /etc/discovery-client

%clean
rm -rf %{buildroot}

%changelog
* Tue Oct 18 2022 Muli Ben-Yehuda <muli@lightbitslabs.com>
- Split discovery-client into its own package

* Thu Mar 19 2020 Yogev Cohen <yogev@lightbitslabs.com> - 0.2-1
- Add discovery client package

* Sun Feb 9 2020 Yogev Cohen <yogev@lightbitslabs.com> - 0.1-1
- Initial discovery service package
