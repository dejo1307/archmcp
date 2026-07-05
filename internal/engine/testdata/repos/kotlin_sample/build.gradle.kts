// Root build script. The Kotlin extractor's Detect() only requires that a root
// build.gradle(.kts) contains the substring "kotlin" or "android".
plugins {
    kotlin("jvm") version "1.9.0" apply false
}

allprojects {
    repositories {
        google()
        mavenCentral()
    }
}
