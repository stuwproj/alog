Feature: Retention and truncation
  A log can be shortened from either end, and has configurable limits.

  Scenario: Truncating before a sequence drops the oldest records
    Given an empty log
    And the log contains "a", "b", "c", "d"
    When I truncate before sequence 2
    Then the first sequence is 2
    And the next sequence is 4
    And scanning the log yields "c", "d"

  Scenario: Truncating after a sequence drops the newest records
    Given an empty log
    And the log contains "a", "b", "c", "d"
    When I truncate after sequence 1
    Then the last sequence is 1
    And scanning the log yields "a", "b"

  Scenario: Sequences are reused after truncating the end
    Given an empty log
    And the log contains "a", "b", "c"
    When I truncate after sequence 0
    And I append "x"
    Then the last sequence is 1
    And scanning the log yields "a", "x"

  Scenario: Sequences are never reused after truncating the start
    Given an empty log
    And the log contains "a", "b", "c"
    When I truncate before sequence 2
    And I append "d"
    Then the last sequence is 3
    And scanning the log yields "c", "d"

  Scenario: Truncation is persistent
    Given an empty log
    And the log contains "a", "b", "c", "d"
    When I truncate before sequence 1
    And I truncate after sequence 2
    And I close and reopen the log
    Then scanning the log yields "b", "c"

  Scenario: A log limited to one segment becomes full
    Given an empty log with segments of 1024 bytes and at most 1 segment
    When I append records of 100 bytes until the append fails
    Then the append fails with ErrSegmentFull
    And the log is still readable

  Scenario: A log rolls over to new segments
    Given an empty log with segments of 1024 bytes
    When I append 50 records of 100 bytes
    Then the log holds 50 records
    And the log uses more than one segment file

  Scenario: Truncating before a sequence frees disk space
    Given an empty log with segments of 1024 bytes
    And the log contains 50 records of 100 bytes
    When I truncate before sequence 40
    Then the log uses less disk space than before
    And the first sequence is at most 40
    And the sequences are consecutive
