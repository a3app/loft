cmake_minimum_required(VERSION 3.5)

project(app.loft.aurora
    LANGUAGES C CXX
    VERSION 1.0.0
    DESCRIPTION "Loft overlay client"
)

set(CMAKE_CXX_STANDARD 11)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_AUTOMOC ON)
set(CMAKE_INCLUDE_CURRENT_DIR ON)

find_package(Qt5 COMPONENTS Core Network Qml Quick REQUIRED)
find_package(Qt5 COMPONENTS DBus QUIET)
find_package(PkgConfig)

include(GNUInstallDirs)
include(CheckIncludeFile)

if(PKG_CONFIG_FOUND)
    pkg_check_modules(AURORA QUIET auroraapp)
    pkg_check_modules(CONNMAN QUIET connman)
    pkg_check_modules(GLIB QUIET glib-2.0)
    pkg_check_modules(DBUS QUIET dbus-1)
endif()

set(LOFT_LIBEXEC "${CMAKE_INSTALL_FULL_LIBEXECDIR}/${PROJECT_NAME}")
set(LOFT_HELPER "${LOFT_LIBEXEC}/loft-connman")
set(LOFT_FRONT_BIN "${LOFT_LIBEXEC}/loft-front")
set(LOFT_SCRIPT_BIN "${CMAKE_INSTALL_FULL_LIBDIR}/connman/scripts/loft-script")

add_executable(${PROJECT_NAME}
    src/main.cpp
    src/engine.cpp
    src/engine.h
)

target_compile_definitions(${PROJECT_NAME} PRIVATE
    APP_ID="app.loft.aurora"
)

target_link_libraries(${PROJECT_NAME}
    Qt5::Core
    Qt5::Network
    Qt5::Qml
    Qt5::Quick
)

if(Qt5DBus_FOUND)
    target_compile_definitions(${PROJECT_NAME} PRIVATE HAVE_QT_DBUS)
    target_link_libraries(${PROJECT_NAME} Qt5::DBus)
endif()

if(AURORA_FOUND)
    target_compile_definitions(${PROJECT_NAME} PRIVATE AURORA)
    target_include_directories(${PROJECT_NAME} PRIVATE ${AURORA_INCLUDE_DIRS})
    target_link_libraries(${PROJECT_NAME} ${AURORA_LIBRARIES})
endif()

install(TARGETS ${PROJECT_NAME}
    RUNTIME DESTINATION ${CMAKE_INSTALL_BINDIR}
)
install(DIRECTORY qml
    DESTINATION ${CMAKE_INSTALL_DATADIR}/${PROJECT_NAME}
)
install(FILES app.loft.aurora.desktop
    DESTINATION ${CMAKE_INSTALL_DATADIR}/applications
)

set(LOFT_MARK "${CMAKE_CURRENT_SOURCE_DIR}/../../docs/brand/svg/loft-icon.svg")
if(EXISTS "${LOFT_MARK}")
    install(FILES "${LOFT_MARK}"
        DESTINATION ${CMAKE_INSTALL_DATADIR}/icons/hicolor/scalable/apps
        RENAME app.loft.aurora.svg
    )
endif()

if(EXISTS "${CMAKE_CURRENT_SOURCE_DIR}/bin/loft-front")
    install(PROGRAMS bin/loft-front
        DESTINATION ${CMAKE_INSTALL_LIBEXECDIR}/${PROJECT_NAME}
    )
endif()

add_executable(loft-connman connman/loft-connman.c)
target_compile_definitions(loft-connman PRIVATE
    LOFT_FRONT="${LOFT_FRONT_BIN}"
    LOFT_SCRIPT="${LOFT_SCRIPT_BIN}"
)
install(TARGETS loft-connman
    RUNTIME DESTINATION ${CMAKE_INSTALL_LIBEXECDIR}/${PROJECT_NAME}
)

if(DBUS_FOUND)
    add_executable(loft-script connman/loft-script.c)
    target_include_directories(loft-script PRIVATE ${DBUS_INCLUDE_DIRS})
    target_link_libraries(loft-script ${DBUS_LIBRARIES})
    install(TARGETS loft-script
        RUNTIME DESTINATION ${CMAKE_INSTALL_LIBDIR}/connman/scripts
    )
endif()

if(CONNMAN_FOUND AND GLIB_FOUND AND DBUS_FOUND)
    add_library(loft-vpn MODULE connman/loft-plugin.c)
    set_target_properties(loft-vpn PROPERTIES
        PREFIX ""
        OUTPUT_NAME loft
        SUFFIX ".so"
    )
    target_compile_definitions(loft-vpn PRIVATE
        CONNMAN_PLUGIN
        CONNMAN_API_SUBJECT_TO_CHANGE
        LOFT_HELPER="${LOFT_HELPER}"
        SCRIPTDIR="${CMAKE_INSTALL_FULL_LIBDIR}/connman/scripts"
    )
    target_include_directories(loft-vpn PRIVATE
        ${CONNMAN_INCLUDE_DIRS}
        ${GLIB_INCLUDE_DIRS}
        ${DBUS_INCLUDE_DIRS}
    )
    set(CMAKE_REQUIRED_INCLUDES ${CONNMAN_INCLUDE_DIRS})
    check_include_file("connman/vpn/plugins/vpn.h" HAVE_CONNMAN_VPN_PLUGINS_VPN_H)
    if(HAVE_CONNMAN_VPN_PLUGINS_VPN_H)
        target_compile_definitions(loft-vpn PRIVATE HAVE_CONNMAN_VPN_PLUGINS_VPN_H)
    endif()
    target_link_libraries(loft-vpn
        ${CONNMAN_LIBRARIES}
        ${GLIB_LIBRARIES}
        ${DBUS_LIBRARIES}
    )
    install(TARGETS loft-vpn
        LIBRARY DESTINATION ${CMAKE_INSTALL_LIBDIR}/connman/plugins-vpn
    )
    install(FILES connman/loft.conf
        DESTINATION ${CMAKE_INSTALL_SYSCONFDIR}/connman/vpn-plugin
    )
endif()

install(DIRECTORY settings/
    DESTINATION ${CMAKE_INSTALL_DATADIR}/sailfish-vpn/loft
    FILES_MATCHING PATTERN "*.qml"
)
