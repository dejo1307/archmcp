# frozen_string_literal: true

# Exercises v72 nested-Rails-resource path shapes.
Rails.application.routes.draw do
  namespace :admin do
    resources :reports, only: [:index, :show] do
      # v72: a singular `resource` nested in a plural `resources` nests under the
      # parent member (`/:report_id`) and has NO `:id` of its own.
      #   -> /admin/reports/:report_id/export  (GET show, POST create)
      resource :export, only: [:show, :create]

      # v72: a plural `resources` nested in a plural `resources` nests under the
      # parent member id (`/:report_id`) and keeps its own `:id`.
      #   -> /admin/reports/:report_id/sections  and  /:id
      resources :sections, only: [:index, :show]
    end
  end

  # Top-level singular resource: /session with no :id member path.
  resource :session, only: [:show, :create, :destroy]

  root to: "home#index"
end
