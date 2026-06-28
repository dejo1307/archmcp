<?php
/**
 * A widget class that extends WordPress core and reuses a helper from inc/,
 * producing a cross-module coupling edge (inc/widgets -> inc).
 */

class Acme_Widget extends WP_Widget {
    public function widget($args, $instance) {
        echo acme_format($instance['title']);
    }
}
