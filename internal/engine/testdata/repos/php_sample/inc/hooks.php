<?php
/**
 * Hook registrations: actions and filters wired to plugin callbacks.
 */

add_action('init', 'acme_setup');
add_filter('the_title', 'acme_format');

function acme_setup() {
    register_widget('Acme_Widget');
}
