//! API layer that depends on the db module.

use crate::db::get_user;

pub fn handler(user_id: u32) -> String {
    let user = get_user(user_id);
    user.name
}

/// Registered through `utoipa_axum::routes!(get_user_endpoint)`, which repeats
/// the handler but not its path: the path is only ever written here.
#[utoipa::path(
    get,
    path = "/api/v1/users/{id}",
    params(("id" = u32, Path, description = "user id")),
    responses((status = 200, description = "the user")),
)]
pub fn get_user_endpoint(user_id: u32) -> String {
    handler(user_id)
}
