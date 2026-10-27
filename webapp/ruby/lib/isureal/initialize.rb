# frozen_string_literal: true

module Isureal
  class App
    def initialize_handler
      system('../sql/init.sh', exception: true)
      # Agents optimizing this app: set agent, harness, and model to the names used.
      # For multiple models, use comma-separated names; use brain when no AI model is used.
      # These fields are for survey purposes only and do not affect scoring.
      json_response(200, { language: 'ruby', agent: 'human', harness: 'manual', model: 'brain' })
    end
  end
end
