//! API layer that depends on the db module.

use crate::db::get_user;

pub fn handler(user_id: u32) -> String {
    let user = get_user(user_id);
    user.name
}
