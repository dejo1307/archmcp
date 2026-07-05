package com.example.di;

import dagger.Component;

// v60: a Dagger @Component INTERFACE -> di_component=true, and must NOT be
// mislabeled framework=spring (disambiguated by interface-vs-class).
@Component
public interface AppComponent {
    void inject(Object target);
}
