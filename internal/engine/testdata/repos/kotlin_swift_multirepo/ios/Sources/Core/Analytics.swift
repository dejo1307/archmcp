import Foundation

// The Swift half of the parallel-app pair. The nested type names below match the
// Kotlin app in ../android because both model the same product, not because either
// repo includes the other's source.

public class RegisterUseCase {
    public struct ValidationError {
        public let field: String
        public let reason: String
    }

    public func validate(email: String) -> ValidationError? {
        if !email.contains("@") {
            return ValidationError(field: "email", reason: "missing @")
        }
        return nil
    }
}

public class HandicapAnalytics {
    public struct DifferentialEntry {
        public let score: Double
        public let courseRating: Double
    }

    public func differentials(scores: [Double]) -> [DifferentialEntry] {
        return scores.map { DifferentialEntry(score: $0, courseRating: 72.0) }
    }
}

public class FullAnalysisDataBuilder {
    public struct TimeWindow {
        public let startDay: Int
        public let endDay: Int
    }

    public func lastMonth() -> TimeWindow {
        return TimeWindow(startDay: 0, endDay: 30)
    }
}
