plugins {
    id("com.android.application") version "8.7.3" apply false
}

layout.buildDirectory.set(
    rootProject.projectDir.resolve("../../dist/clients/Android/gradle/root").canonicalFile
)
