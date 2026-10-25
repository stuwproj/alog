Feature: Concurrent producers
  A writer groups appends from concurrent producers into batches
  without losing or duplicating records.

  Scenario: Concurrent producers get unique sequences
    Given an empty log
    And a started writer
    When 8 producers each append 50 records through the writer
    Then the log holds 400 records
    And every producer received distinct sequences
    And the sequences are consecutive

  Scenario: Records of one producer keep their order
    Given an empty log
    And a started writer
    When 4 producers each append 100 records through the writer
    Then the records of each producer are in the order they were appended

  Scenario: Readers can scan while producers append
    Given an empty log
    And a started writer
    When 4 producers each append 100 records through the writer while a reader scans
    Then the reader saw consecutive sequences
    And the log holds 400 records

  Scenario: A cancelled writer context stops appends
    Given an empty log
    And a started writer
    When the writer is stopped
    Then appending through the writer fails
