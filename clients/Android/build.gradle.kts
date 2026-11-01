plugins {
    id("com.android.application") version "8.13.2" apply false
}

layout.buildDirectory.set(
    rootProject.projectDir.resolve("../../dist/clients/Android/gradle/root").canonicalFile
)
