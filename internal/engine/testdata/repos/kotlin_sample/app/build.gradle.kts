plugins {
    kotlin("android")
}

android {
    // Base package for the app module — read by the Kotlin extractor's
    // detectKotlinBasePackage() as the internal-import fallback root.
    namespace = "de.foo.app"
}

dependencies {
    implementation(project(":api"))
}
