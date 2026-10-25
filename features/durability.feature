Feature: Durability and exclusive access
  What was appended survives closing the log, and a directory is
  used by one log at a time.

  Scenario: Records survive a reopen
    Given an empty log
    And the log contains "a", "b", "c"
    When I close and reopen the log
    Then scanning the log yields "a", "b", "c"
    And the last sequence is 2

  Scenario: Appending continues after a reopen
    Given an empty log
    And the log contains "a", "b"
    When I close and reopen the log
    And I append "c"
    Then the last sequence is 2
    And scanning the log yields "a", "b", "c"

  Scenario: Records survive a reopen across segments
    Given an empty log with segments of 1024 bytes
    And the log contains 50 records of 100 bytes
    When I close and reopen the log
    Then the log holds 50 records
    And the sequences are consecutive

  Scenario: The sequence space of an existing log is kept
    Given an empty log starting at sequence 10
    And the log contains "a", "b"
    When I close and reopen the log with start sequence 500
    Then the first sequence is 10
    And the next sequence is 12

  Scenario: Syncing does not change what is stored
    Given an empty log
    And the log contains "a", "b"
    When I sync the log
    Then scanning the log yields "a", "b"

  Scenario: A directory is used by one log at a time
    Given an empty log
    When I open a second log on the same directory
    Then opening fails with ErrLocked

  Scenario: The directory can be reused once the log is closed
    Given an empty log
    And the log contains "a"
    When I close the log
    And I open a second log on the same directory
    Then opening succeeds

  Scenario: A read only log can not be appended to
    Given an empty log
    And the log contains "a"
    When I close and reopen the log read only
    Then scanning the log yields "a"
    And appending "b" fails

  Scenario: Records appended without preallocation can be scanned
    Given an empty log without preallocation
    And the log contains "a", "b"
    When I close and reopen the log
    And I append "c"
    Then scanning the log yields "a", "b", "c"

  Scenario: A scanner follows a log without preallocation
    Given an empty log without preallocation
    And the log contains "a", "b"
    And a scanner that has read everything
    When I append "c"
    Then scanning yields "c"
