Feature: Appending records
  A log assigns consecutive sequence numbers to the records appended to it,
  and a batch is written as one unit.

  Scenario: A new log holds no records
    Given an empty log
    Then the log has no records
    And the next sequence is 0

  Scenario: Sequences are consecutive
    Given an empty log
    When I append "a"
    And I append "b"
    And I append "c"
    Then the first sequence is 0
    And the last sequence is 2
    And the next sequence is 3

  Scenario: A batch is assigned consecutive sequences
    Given an empty log
    When I append the batch "a", "b", "c"
    Then the append returned sequence 2
    And scanning the log yields "a", "b", "c"

  Scenario: A multipart record is stored as one record
    Given an empty log
    When I append the multipart record "head", "-", "tail"
    Then the last sequence is 0
    And scanning the log yields "head-tail"

  Scenario: A new log can start at a chosen sequence
    Given an empty log starting at sequence 100
    When I append "a"
    Then the first sequence is 100
    And the last sequence is 100

  Scenario: Empty payloads are records too
    Given an empty log
    When I append ""
    Then the last sequence is 0
    And scanning the log yields ""

  Scenario: A record larger than the maximum payload size is rejected
    Given an empty log with a maximum payload size of 16 bytes
    And the log contains "keep"
    When I append a record of 17 bytes
    Then the append fails with ErrPayloadTooLarge
    And the log has not changed
    And scanning the log yields "keep"

  Scenario: A batch that cannot fit in an empty segment is rejected as a whole
    Given an empty log with segments of 1024 bytes
    When I append a batch of 20 records of 100 bytes
    Then the append fails with ErrBatchTooLarge
    And the log has no records
