val androidDist = settingsDir.resolve("../../dist/clients/Android").canonicalFile
androidDist.mkdirs()
gradle.startParameter.projectCacheDir = androidDist.resolve(".gradle")

pluginManagement {
    repositories {
        google()
        mavenCentral()
        gradlePluginPortal()
    }
}
dependencyResolutionManagement {
    repositoriesMode.set(RepositoriesMode.FAIL_ON_PROJECT_REPOS)
    repositories {
        google()
        mavenCentral()
    }
}
rootProject.name = "loft"
include(":app")
