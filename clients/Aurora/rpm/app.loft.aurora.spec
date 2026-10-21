Name:       app.loft.aurora
Summary:    Loft overlay client
Version:    1.0.0
Release:    1
License:    Apache-2.0
URL:        https://github.com/a3app/loft
Source0:    %{name}-%{version}.tar.bz2
BuildRequires:  pkgconfig(Qt5Core)
BuildRequires:  pkgconfig(Qt5Network)
BuildRequires:  pkgconfig(Qt5Qml)
BuildRequires:  pkgconfig(Qt5Quick)
BuildRequires:  pkgconfig(Qt5DBus)
BuildRequires:  pkgconfig(dbus-1)
BuildRequires:  pkgconfig(glib-2.0)
BuildRequires:  pkgconfig(connman)
Requires:   sailfishsilica-qt5
Requires:   connman

%description
Loft overlay client for Aurora OS. The QML app writes YAML. ConnMan
owns the tun via the loft VPN plugin; loft-front is the session.

%prep
%setup -q -n %{name}-%{version}

%build
%cmake
%cmake_build

%install
%cmake_install

%files
%{_bindir}/%{name}
%{_datadir}/%{name}
%{_datadir}/applications/%{name}.desktop
%{_datadir}/icons/hicolor/scalable/apps/%{name}.svg
%{_libexecdir}/%{name}/loft-front
%{_libexecdir}/%{name}/loft-connman
%{_libdir}/connman/plugins-vpn/loft.so
%{_libdir}/connman/scripts/loft-script
%{_datadir}/sailfish-vpn/loft
%config(noreplace) %{_sysconfdir}/connman/vpn-plugin/loft.conf
